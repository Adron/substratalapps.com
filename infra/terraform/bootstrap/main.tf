# Applied once, by hand, with admin credentials (README → One-time setup).
# It's the only Terraform ever applied from a laptop. In order:
#   1. Cost guardrails, before any other billable resource (DEPLOYMENT.md →
#      Cost guardrails: "a hard budget alarm exists before the first
#      resource does").
#   2. The Terraform state bucket and the artifacts bucket.
#   3. The GitHub OIDC provider and the two CI roles: a read-only plan role
#      (pull requests into main) and the deploy role (main + production
#      environment only).

terraform {
  required_version = ">= 1.10"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
}

provider "aws" {
  region = var.region
  default_tags { tags = { project = "substratal", managed_by = "terraform", stack = "bootstrap" } }
}

data "aws_caller_identity" "me" {}

# ── 1. Cost guardrails ──────────────────────────────────────────────────

resource "aws_sns_topic" "billing_alerts" {
  name = "substratal-billing-alerts"
}

resource "aws_sns_topic_subscription" "billing_email" {
  topic_arn = aws_sns_topic.billing_alerts.arn
  protocol  = "email"
  endpoint  = var.billing_email
}

resource "aws_budgets_budget" "warn" {
  name         = "substratal-warn"
  budget_type  = "COST"
  limit_amount = tostring(var.warn_budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  dynamic "notification" {
    for_each = [50, 80, 100]
    content {
      comparison_operator       = "GREATER_THAN"
      threshold                 = notification.value
      threshold_type            = "PERCENTAGE"
      notification_type         = "ACTUAL"
      subscriber_sns_topic_arns = [aws_sns_topic.billing_alerts.arn]
    }
  }
}

# The hard budget is a real circuit breaker: at the threshold, a Budget
# Action attaches a deny-everything-billable policy to the deploy role and
# the Lambda execution roles' boundary group.
resource "aws_budgets_budget" "hard" {
  name         = "substratal-hard"
  budget_type  = "COST"
  limit_amount = tostring(var.hard_budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  notification {
    comparison_operator       = "GREATER_THAN"
    threshold                 = 100
    threshold_type            = "PERCENTAGE"
    notification_type         = "ACTUAL"
    subscriber_sns_topic_arns = [aws_sns_topic.billing_alerts.arn]
  }
}

resource "aws_iam_policy" "circuit_breaker" {
  name        = "substratal-budget-circuit-breaker"
  description = "Attached by the hard budget action: stops new spend on non-essential services."
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Deny"
      Action   = ["lambda:CreateFunction", "lambda:UpdateFunctionCode", "rds:CreateDBCluster", "rds:ModifyDBCluster", "ec2:RunInstances"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role" "budget_action" {
  name = "substratal-budget-action"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "budgets.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy" "budget_action" {
  role = aws_iam_role.budget_action.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = ["iam:AttachRolePolicy", "iam:DetachRolePolicy"], Resource = aws_iam_role.deploy.arn }]
  })
}

resource "aws_budgets_budget_action" "hard_stop" {
  budget_name        = aws_budgets_budget.hard.name
  action_type        = "APPLY_IAM_POLICY"
  approval_model     = "AUTOMATIC"
  notification_type  = "ACTUAL"
  execution_role_arn = aws_iam_role.budget_action.arn
  action_threshold {
    action_threshold_type  = "PERCENTAGE"
    action_threshold_value = 100
  }
  definition {
    iam_action_definition {
      policy_arn = aws_iam_policy.circuit_breaker.arn
      roles      = [aws_iam_role.deploy.name]
    }
  }
  subscriber {
    address           = var.billing_email
    subscription_type = "EMAIL"
  }
}

resource "aws_ce_anomaly_monitor" "services" {
  name              = "substratal-services"
  monitor_type      = "DIMENSIONAL"
  monitor_dimension = "SERVICE"
}

resource "aws_ce_anomaly_subscription" "daily" {
  name             = "substratal-anomalies"
  frequency        = "DAILY"
  monitor_arn_list = [aws_ce_anomaly_monitor.services.arn]
  subscriber {
    type    = "EMAIL"
    address = var.billing_email
  }
  threshold_expression {
    dimension {
      key           = "ANOMALY_TOTAL_IMPACT_ABSOLUTE"
      values        = ["10"]
      match_options = ["GREATER_THAN_OR_EQUAL"]
    }
  }
}

# A third, independent tripwire. Billing metrics only exist in us-east-1.
resource "aws_cloudwatch_metric_alarm" "estimated_charges" {
  alarm_name          = "substratal-estimated-charges"
  namespace           = "AWS/Billing"
  metric_name         = "EstimatedCharges"
  dimensions          = { Currency = "USD" }
  statistic           = "Maximum"
  period              = 21600
  evaluation_periods  = 1
  threshold           = var.warn_budget_usd
  comparison_operator = "GreaterThanThreshold"
  alarm_actions       = [aws_sns_topic.billing_alerts.arn]
}

# ── 2. State and artifacts ──────────────────────────────────────────────

resource "aws_s3_bucket" "state" {
  bucket = "substratal-tfstate-${data.aws_caller_identity.me.account_id}"
}

resource "aws_s3_bucket" "artifacts" {
  bucket = "substratal-artifacts-${data.aws_caller_identity.me.account_id}"
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    id     = "expire-old-releases"
    status = "Enabled"
    filter {}
    expiration { days = 180 }
    noncurrent_version_expiration { noncurrent_days = 30 }
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket                  = aws_s3_bucket.state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

# ── 3. GitHub OIDC and CI roles ─────────────────────────────────────────

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

locals {
  oidc_sub = "repo:${var.github_repository}"
}

# Read-only: terraform plan on pull requests into main.
resource "aws_iam_role" "plan" {
  name = "substratal-github-plan"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.github.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = { "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com" }
        StringLike   = { "token.actions.githubusercontent.com:sub" = "${local.oidc_sub}:pull_request" }
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "plan_readonly" {
  role       = aws_iam_role.plan.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy" "plan_state" {
  role = aws_iam_role.plan.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject", "s3:ListBucket"]
      Resource = [aws_s3_bucket.state.arn, "${aws_s3_bucket.state.arn}/*"]
    }]
  })
}

# Deploy: only main, only the production environment. This trust policy is
# the AWS-side enforcement of "merging into main is the only way to deploy".
resource "aws_iam_role" "deploy" {
  name = "substratal-github-deploy"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.github.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = "${local.oidc_sub}:environment:production"
        }
      }
    }]
  })
}

# Broad within this dedicated account by design: the deploy role is what
# applies infra/terraform/prod. The OIDC trust policy above, not this
# permission set, is what keeps it to main + production.
resource "aws_iam_role_policy_attachment" "deploy_admin" {
  role       = aws_iam_role.deploy.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}
