# Copy these into GitHub repository variables (README → One-time setup).
output "AWS_PLAN_ROLE_ARN" { value = aws_iam_role.plan.arn }
output "AWS_DEPLOY_ROLE_ARN" { value = aws_iam_role.deploy.arn }
output "TF_STATE_BUCKET" { value = aws_s3_bucket.state.bucket }
output "ARTIFACT_BUCKET" { value = aws_s3_bucket.artifacts.bucket }
output "AWS_REGION" { value = var.region }
