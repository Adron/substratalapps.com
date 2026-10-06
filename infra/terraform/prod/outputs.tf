# deploy.yml reads these to run migrations against the cluster.
output "db_cluster_arn" { value = aws_rds_cluster.main.arn }
output "db_secret_arn" { value = local.db_secret_arn }
output "api_endpoint" { value = aws_apigatewayv2_api.main.api_endpoint }
output "api_url" { value = "https://${var.domain_name}" }
output "app_secret_arn" { value = aws_secretsmanager_secret.app.arn }
output "webhook_queue_url" { value = aws_sqs_queue.webhooks.url }
output "audit_archive_bucket" { value = aws_s3_bucket.audit_archive.bucket }
