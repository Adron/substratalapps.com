# Production: the Tier 0 stack from DEPLOYMENT.md → First deployment.
# No VPC for Lambda and no NAT Gateway: every function reaches Aurora over
# the RDS Data API (IAM-signed HTTPS), and the webhook worker gets outbound
# internet access for free by staying outside a VPC.

locals {
  prefix    = "substratal-prod"
  functions = ["api", "mcp", "stripe-webhook", "webhook-worker", "jobs"]
  artifact  = { for f in local.functions : f => "${var.release}/${f}.zip" }
  db_name   = "substratal"
}

data "aws_caller_identity" "me" {}
data "aws_route53_zone" "main" { name = var.hosted_zone }

# ── Keys (DEPLOYMENT.md → Build checklist, step 3) ─────────────────────

# JWT signing: the private key never leaves KMS; JWKS publishes its public half.
resource "aws_kms_key" "signing" {
  description              = "Substratal JWT signing (RS256)"
  key_usage                = "SIGN_VERIFY"
  customer_master_key_spec = "RSA_2048"
  deletion_window_in_days  = 30
}

resource "aws_kms_alias" "signing" {
  name          = "alias/${local.prefix}-jwt-signing"
  target_key_id = aws_kms_key.signing.key_id
}

# TOTP and webhook signing secrets: the server must read them back.
resource "aws_kms_key" "data" {
  description             = "Substratal secret encryption (TOTP, webhook secrets)"
  enable_key_rotation     = true
  deletion_window_in_days = 30
}

resource "aws_kms_alias" "data" {
  name          = "alias/${local.prefix}-data"
  target_key_id = aws_kms_key.data.key_id
}

# ── Database: Aurora Serverless v2 with the Data API ───────────────────

resource "aws_rds_cluster" "main" {
  cluster_identifier          = "${local.prefix}-db"
  engine                      = "aurora-postgresql"
  engine_mode                 = "provisioned"
  engine_version              = "16.6"
  database_name               = local.db_name
  master_username             = "substratal"
  manage_master_user_password = true # Secrets Manager–managed; the Data API authenticates with it
  enable_http_endpoint        = true # the Data API
  storage_encrypted           = true
  backup_retention_period     = 7
  deletion_protection         = true
  copy_tags_to_snapshot       = true
  final_snapshot_identifier   = "${local.prefix}-db-final"
  serverlessv2_scaling_configuration {
    min_capacity = var.db_min_acu
    max_capacity = var.db_max_acu
  }
}

resource "aws_rds_cluster_instance" "main" {
  identifier         = "${local.prefix}-db-1"
  cluster_identifier = aws_rds_cluster.main.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.main.engine
  engine_version     = aws_rds_cluster.main.engine_version
}

locals {
  db_secret_arn = aws_rds_cluster.main.master_user_secret[0].secret_arn
}

# ── Application secret (cursor key, Stripe keys) ───────────────────────

resource "random_password" "cursor" {
  length  = 48
  special = false
}

resource "random_password" "mcp_session" {
  length  = 48
  special = false
}

resource "aws_secretsmanager_secret" "app" {
  name        = "${local.prefix}/app"
  description = "cursor_secret, stripe_secret_key, stripe_webhook_secret. Set the Stripe keys by hand; Terraform never overwrites them."
}

resource "aws_secretsmanager_secret_version" "app" {
  secret_id = aws_secretsmanager_secret.app.id
  secret_string = jsonencode({
    cursor_secret         = random_password.cursor.result
    stripe_secret_key     = ""
    stripe_webhook_secret = ""
  })
  lifecycle { ignore_changes = [secret_string] }
}

# ── Queues and storage ─────────────────────────────────────────────────

resource "aws_sqs_queue" "webhooks_dlq" {
  name                      = "${local.prefix}-webhooks-dlq"
  message_retention_seconds = 1209600
}

# Nudges only: the message is an outbox event id. The worker always drains
# the outbox, so a lost message just waits for the 1-minute sweep.
resource "aws_sqs_queue" "webhooks" {
  name                       = "${local.prefix}-webhooks"
  visibility_timeout_seconds = 120
  redrive_policy             = jsonencode({ deadLetterTargetArn = aws_sqs_queue.webhooks_dlq.arn, maxReceiveCount = 5 })
}

resource "aws_s3_bucket" "audit_archive" {
  bucket = "${local.prefix}-audit-archive-${data.aws_caller_identity.me.account_id}"
}

resource "aws_s3_bucket_public_access_block" "audit_archive" {
  bucket                  = aws_s3_bucket.audit_archive.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "audit_archive" {
  bucket = aws_s3_bucket.audit_archive.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

resource "aws_s3_bucket_versioning" "audit_archive" {
  bucket = aws_s3_bucket.audit_archive.id
  versioning_configuration { status = "Enabled" }
}

# ── Email (SES) ────────────────────────────────────────────────────────

resource "aws_sesv2_email_identity" "mail" {
  email_identity = var.mail_domain
}

resource "aws_route53_record" "dkim" {
  count   = 3
  zone_id = data.aws_route53_zone.main.zone_id
  name    = "${aws_sesv2_email_identity.mail.dkim_signing_attributes[0].tokens[count.index]}._domainkey.${var.mail_domain}"
  type    = "CNAME"
  ttl     = 1800
  records = ["${aws_sesv2_email_identity.mail.dkim_signing_attributes[0].tokens[count.index]}.dkim.amazonses.com"]
}

resource "aws_route53_record" "dmarc" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = "_dmarc.${var.mail_domain}"
  type    = "TXT"
  ttl     = 1800
  records = ["v=DMARC1; p=quarantine; rua=mailto:dmarc@${var.hosted_zone}"]
}

# ── Function permissions ───────────────────────────────────────────────

locals {
  data_api = [{
    Effect   = "Allow"
    Action   = ["rds-data:ExecuteStatement", "rds-data:BatchExecuteStatement", "rds-data:BeginTransaction", "rds-data:CommitTransaction", "rds-data:RollbackTransaction"]
    Resource = aws_rds_cluster.main.arn
  }]
  secrets = [{
    Effect   = "Allow"
    Action   = ["secretsmanager:GetSecretValue"]
    Resource = [local.db_secret_arn, aws_secretsmanager_secret.app.arn]
  }]
  sign = [{
    Effect   = "Allow"
    Action   = ["kms:Sign", "kms:GetPublicKey"]
    Resource = aws_kms_key.signing.arn
  }]
  verify_only = [{
    Effect   = "Allow"
    Action   = ["kms:GetPublicKey"]
    Resource = aws_kms_key.signing.arn
  }]
  crypt = [{
    Effect   = "Allow"
    Action   = ["kms:Encrypt", "kms:Decrypt"]
    Resource = aws_kms_key.data.arn
  }]
  email = [{
    Effect   = "Allow"
    Action   = ["ses:SendEmail"]
    Resource = aws_sesv2_email_identity.mail.arn
  }]

  common_env = {
    APP_ENV            = "production"
    DATABASE_BACKEND   = "dataapi"
    DB_CLUSTER_ARN     = aws_rds_cluster.main.arn
    DB_SECRET_ARN      = local.db_secret_arn
    DB_NAME            = local.db_name
    ISSUER             = "https://${var.domain_name}"
    PUBLIC_BASE_URL    = "https://${var.domain_name}"
    AUTHORIZE_URL      = var.authorize_url
    DASHBOARD_URL      = var.dashboard_url
    KMS_SIGNING_KEY_ID = aws_kms_key.signing.arn
    KMS_DATA_KEY_ID    = aws_kms_key.data.arn
    APP_SECRET_ARN     = aws_secretsmanager_secret.app.arn
    EMAIL_BACKEND      = "ses"
    EMAIL_FROM         = "Substratal <no-reply@${var.mail_domain}>"
  }
}

module "api" {
  source          = "../modules/lambda_function"
  name            = "${local.prefix}-api"
  artifact_bucket = var.artifact_bucket
  artifact_key    = local.artifact["api"]
  memory_mb       = 512 # Argon2id at m=19 MiB per login, with headroom
  timeout_s       = 29
  # The security log lives here, and NFR → Security logging keeps it a year.
  log_retention_days = 365
  environment = merge(local.common_env, {
    WEBHOOK_QUEUE_URL    = aws_sqs_queue.webhooks.url
    CORS_ALLOWED_ORIGINS = var.cors_origins
  })
  policy_statements = concat(local.data_api, local.secrets, local.sign, local.crypt, local.email, [{
    Effect = "Allow", Action = ["sqs:SendMessage"], Resource = aws_sqs_queue.webhooks.arn
  }])
}

# The MCP server holds no data-plane permissions at all: it only calls the
# API's own /v1 routes over HTTPS with the caller's credential.
module "mcp" {
  source          = "../modules/lambda_function"
  name            = "${local.prefix}-mcp"
  artifact_bucket = var.artifact_bucket
  artifact_key    = local.artifact["mcp"]
  memory_mb       = 256
  timeout_s       = 29
  environment = {
    PUBLIC_BASE_URL = "https://${var.domain_name}"
    MCP_SESSION_KEY = random_password.mcp_session.result
  }
}

module "stripe_webhook" {
  source            = "../modules/lambda_function"
  name              = "${local.prefix}-stripe-webhook"
  artifact_bucket   = var.artifact_bucket
  artifact_key      = local.artifact["stripe-webhook"]
  memory_mb         = 256
  timeout_s         = 29
  environment       = local.common_env
  policy_statements = concat(local.data_api, local.secrets, local.verify_only, local.crypt)
}

module "webhook_worker" {
  source               = "../modules/lambda_function"
  name                 = "${local.prefix}-webhook-worker"
  artifact_bucket      = var.artifact_bucket
  artifact_key         = local.artifact["webhook-worker"]
  memory_mb            = 256
  timeout_s            = 110
  reserved_concurrency = 5 # isolates delivery load from the API's blast radius
  environment          = local.common_env
  policy_statements = concat(local.data_api, local.secrets, local.verify_only, local.crypt, local.email, [{
    Effect   = "Allow"
    Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    Resource = aws_sqs_queue.webhooks.arn
  }])
}

module "jobs" {
  source          = "../modules/lambda_function"
  name            = "${local.prefix}-jobs"
  artifact_bucket = var.artifact_bucket
  artifact_key    = local.artifact["jobs"]
  memory_mb       = 512
  timeout_s       = 900
  environment     = merge(local.common_env, { AUDIT_ARCHIVE_BUCKET = aws_s3_bucket.audit_archive.bucket })
  policy_statements = concat(local.data_api, local.secrets, local.sign, local.crypt, local.email, [{
    Effect   = "Allow"
    Action   = ["s3:PutObject"]
    Resource = "${aws_s3_bucket.audit_archive.arn}/*"
  }])
}

resource "aws_lambda_event_source_mapping" "webhooks" {
  event_source_arn = aws_sqs_queue.webhooks.arn
  function_name    = module.webhook_worker.alias_arn
  batch_size       = 10
}

# ── API Gateway (HTTP API) ─────────────────────────────────────────────

resource "aws_apigatewayv2_api" "main" {
  name          = local.prefix
  protocol_type = "HTTP"
}

resource "aws_cloudwatch_log_group" "api_access" {
  name              = "/aws/apigateway/${local.prefix}"
  retention_in_days = 30
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.main.id
  name        = "$default"
  auto_deploy = true
  default_route_settings {
    throttling_rate_limit  = var.api_throttle_rate
    throttling_burst_limit = var.api_throttle_rate * 2
  }
  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.api_access.arn
    format = jsonencode({ requestId = "$context.requestId", ip = "$context.identity.sourceIp", method = "$context.httpMethod",
    path = "$context.path", status = "$context.status", latency = "$context.responseLatency", integration = "$context.integrationErrorMessage" })
  }
}

locals {
  integrations = {
    api    = module.api
    mcp    = module.mcp
    stripe = module.stripe_webhook
  }
  routes = {
    "ANY /v1/{proxy+}"              = "api"
    "GET /.well-known/{proxy+}"     = "api"
    "$default"                      = "api"
    "POST /mcp"                     = "mcp"
    "GET /mcp"                      = "mcp"
    "DELETE /mcp"                   = "mcp"
    "POST /internal/stripe/webhook" = "stripe"
  }
}

resource "aws_apigatewayv2_integration" "fn" {
  for_each               = local.integrations
  api_id                 = aws_apigatewayv2_api.main.id
  integration_type       = "AWS_PROXY"
  integration_uri        = each.value.alias_arn
  payload_format_version = "2.0"
  timeout_milliseconds   = 29000
}

resource "aws_apigatewayv2_route" "r" {
  for_each  = local.routes
  api_id    = aws_apigatewayv2_api.main.id
  route_key = each.key
  target    = "integrations/${aws_apigatewayv2_integration.fn[each.value].id}"
}

resource "aws_lambda_permission" "apigw" {
  for_each      = local.integrations
  statement_id  = "apigateway-${each.key}"
  action        = "lambda:InvokeFunction"
  function_name = each.value.name
  qualifier     = "live"
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.main.execution_arn}/*/*"
}

# ── Custom domain ──────────────────────────────────────────────────────

resource "aws_acm_certificate" "api" {
  domain_name       = var.domain_name
  validation_method = "DNS"
  lifecycle { create_before_destroy = true }
}

resource "aws_route53_record" "cert_validation" {
  for_each = { for o in aws_acm_certificate.api.domain_validation_options : o.domain_name => o }
  zone_id  = data.aws_route53_zone.main.zone_id
  name     = each.value.resource_record_name
  type     = each.value.resource_record_type
  records  = [each.value.resource_record_value]
  ttl      = 300
}

resource "aws_acm_certificate_validation" "api" {
  certificate_arn         = aws_acm_certificate.api.arn
  validation_record_fqdns = [for r in aws_route53_record.cert_validation : r.fqdn]
}

resource "aws_apigatewayv2_domain_name" "api" {
  domain_name = var.domain_name
  domain_name_configuration {
    certificate_arn = aws_acm_certificate_validation.api.certificate_arn
    endpoint_type   = "REGIONAL"
    security_policy = "TLS_1_2"
  }
}

resource "aws_apigatewayv2_api_mapping" "api" {
  api_id      = aws_apigatewayv2_api.main.id
  domain_name = aws_apigatewayv2_domain_name.api.id
  stage       = aws_apigatewayv2_stage.default.id
}

resource "aws_route53_record" "api" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = var.domain_name
  type    = "A"
  alias {
    name                   = aws_apigatewayv2_domain_name.api.domain_name_configuration[0].target_domain_name
    zone_id                = aws_apigatewayv2_domain_name.api.domain_name_configuration[0].hosted_zone_id
    evaluate_target_health = false
  }
}

# ── Scheduled jobs (EventBridge Scheduler) ─────────────────────────────

locals {
  schedules = {
    "entitlement-sweep"    = "rate(5 minutes)"
    "webhook-dispatch"     = "rate(1 minute)"
    "erasure-cascade"      = "rate(1 hour)"
    "webhook-health"       = "rate(1 hour)"
    "stripe-customers"     = "rate(1 hour)"
    "stripe-events"        = "rate(10 minutes)"
    "seat-sync"            = "cron(15 0 * * ? *)"
    "cleanup"              = "cron(0 3 * * ? *)"
    "audit-archive"        = "cron(30 3 * * ? *)"
    "settings-projections" = "cron(0 4 * * ? *)"
  }
}

resource "aws_iam_role" "scheduler" {
  name = "${local.prefix}-scheduler"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "scheduler.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy" "scheduler" {
  role = aws_iam_role.scheduler.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "lambda:InvokeFunction", Resource = module.jobs.alias_arn }]
  })
}

resource "aws_scheduler_schedule" "job" {
  for_each                     = local.schedules
  name                         = "${local.prefix}-${each.key}"
  schedule_expression          = each.value
  schedule_expression_timezone = "UTC"
  flexible_time_window { mode = "OFF" }
  target {
    arn      = module.jobs.alias_arn
    role_arn = aws_iam_role.scheduler.arn
    input    = jsonencode({ job = each.key })
    retry_policy { maximum_retry_attempts = 2 }
  }
}

# ── Alarms (DEPLOYMENT.md → Build checklist, step 8) ───────────────────

resource "aws_sns_topic" "alarms" {
  name = "${local.prefix}-alarms"
}

resource "aws_sns_topic_subscription" "alarms" {
  count     = var.alarm_email == "" ? 0 : 1
  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

resource "aws_cloudwatch_metric_alarm" "api_5xx" {
  alarm_name          = "${local.prefix}-api-5xx"
  namespace           = "AWS/ApiGateway"
  metric_name         = "5xx"
  dimensions          = { ApiId = aws_apigatewayv2_api.main.id }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 2
  threshold           = 10
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}

resource "aws_cloudwatch_metric_alarm" "lambda_errors" {
  for_each            = { api = module.api, mcp = module.mcp, stripe = module.stripe_webhook, worker = module.webhook_worker, jobs = module.jobs }
  alarm_name          = "${local.prefix}-${each.key}-errors"
  namespace           = "AWS/Lambda"
  metric_name         = "Errors"
  dimensions          = { FunctionName = each.value.name }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 5
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}

resource "aws_cloudwatch_metric_alarm" "lambda_throttles" {
  for_each            = { api = module.api, worker = module.webhook_worker }
  alarm_name          = "${local.prefix}-${each.key}-throttles"
  namespace           = "AWS/Lambda"
  metric_name         = "Throttles"
  dimensions          = { FunctionName = each.value.name }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}

resource "aws_cloudwatch_metric_alarm" "aurora_near_max" {
  alarm_name          = "${local.prefix}-aurora-acu-near-max"
  namespace           = "AWS/RDS"
  metric_name         = "ServerlessDatabaseCapacity"
  dimensions          = { DBClusterIdentifier = aws_rds_cluster.main.cluster_identifier }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 3
  threshold           = var.db_max_acu * 0.9
  comparison_operator = "GreaterThanOrEqualToThreshold"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}

resource "aws_cloudwatch_metric_alarm" "webhook_dlq" {
  alarm_name          = "${local.prefix}-webhooks-dlq"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.webhooks_dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}

# NFR → Security logging: alarm on spikes in failed logins, refresh-token
# reuse, and destructive_operation_restricted.
locals {
  security_events = {
    failed-logins       = "{ $.msg = \"security\" && $.code = \"invalid_credentials\" }"
    refresh-reuse       = "{ $.msg = \"security\" && $.event = \"refresh_token_reused\" }"
    destructive-blocked = "{ $.msg = \"security\" && $.code = \"destructive_operation_restricted\" }"
  }
}

resource "aws_cloudwatch_log_metric_filter" "security" {
  for_each       = local.security_events
  name           = "${local.prefix}-${each.key}"
  log_group_name = module.api.log_group
  pattern        = each.value
  metric_transformation {
    name      = each.key
    namespace = "Substratal/Security"
    value     = "1"
  }
}

resource "aws_cloudwatch_metric_alarm" "security" {
  for_each            = local.security_events
  alarm_name          = "${local.prefix}-security-${each.key}"
  namespace           = "Substratal/Security"
  metric_name         = each.key
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = each.key == "failed-logins" ? 50 : 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alarms.arn]
}
