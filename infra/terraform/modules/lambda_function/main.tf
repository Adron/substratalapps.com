# One Lambda function the way every Substratal function is built: Go on
# provided.al2023 / arm64, its own execution role scoped to exactly what it
# needs (DEPLOYMENT.md → Build checklist, step 4: no shared mega-role),
# explicit log retention, and a "live" alias that API Gateway, SQS, and
# the scheduler always invoke. Each deploy publishes a new version and
# moves live to it; rollback.yml moves it back.

terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
}

variable "name" { type = string }
variable "artifact_bucket" { type = string }
variable "artifact_key" { type = string }
variable "memory_mb" {
  type    = number
  default = 256
}
variable "timeout_s" {
  type    = number
  default = 30
}
variable "environment" {
  type    = map(string)
  default = {}
}
variable "policy_statements" {
  description = "IAM statements for this function's own role (Resource may be a string or a list, so the type is any)"
  type        = any
  default     = []
}
variable "log_retention_days" {
  type    = number
  default = 30
}
variable "reserved_concurrency" {
  description = "-1 for unreserved"
  type        = number
  default     = -1
}

resource "aws_iam_role" "this" {
  name = var.name
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy" "logs" {
  role = aws_iam_role.this.id
  name = "logs"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = "${aws_cloudwatch_log_group.this.arn}:*"
    }]
  })
}

resource "aws_iam_role_policy" "this" {
  count  = length(var.policy_statements) > 0 ? 1 : 0
  role   = aws_iam_role.this.id
  name   = "function"
  policy = jsonencode({ Version = "2012-10-17", Statement = var.policy_statements })
}

# Explicit retention: logs left at "never expire" are the classic growing
# CloudWatch bill (DEPLOYMENT.md → Tier 0 components).
resource "aws_cloudwatch_log_group" "this" {
  name              = "/aws/lambda/${var.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "this" {
  function_name                  = var.name
  role                           = aws_iam_role.this.arn
  runtime                        = "provided.al2023"
  architectures                  = ["arm64"]
  handler                        = "bootstrap"
  s3_bucket                      = var.artifact_bucket
  s3_key                         = var.artifact_key
  memory_size                    = var.memory_mb
  timeout                        = var.timeout_s
  publish                        = true
  reserved_concurrent_executions = var.reserved_concurrency
  environment { variables = var.environment }
  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.this.name
  }
  depends_on = [aws_iam_role_policy.logs]
}

resource "aws_lambda_alias" "live" {
  name             = "live"
  function_name    = aws_lambda_function.this.function_name
  function_version = aws_lambda_function.this.version
}

output "name" { value = aws_lambda_function.this.function_name }
output "alias_arn" { value = aws_lambda_alias.live.arn }
output "alias_invoke_arn" { value = aws_lambda_alias.live.invoke_arn }
output "role_name" { value = aws_iam_role.this.name }
output "log_group" { value = aws_cloudwatch_log_group.this.name }
