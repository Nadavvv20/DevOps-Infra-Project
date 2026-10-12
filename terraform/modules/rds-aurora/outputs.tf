output "cluster_endpoint" {
  value = var.enable_aurora_cluster ? aws_rds_cluster.this[0].endpoint : aws_db_instance.this[0].address
}

output "cluster_reader_endpoint" {
  value = var.enable_aurora_cluster ? aws_rds_cluster.this[0].reader_endpoint : aws_db_instance.this[0].address
}

output "cluster_identifier" {
  value = var.enable_aurora_cluster ? aws_rds_cluster.this[0].cluster_identifier : aws_db_instance.this[0].identifier
}

output "secret_arn" {
  value = aws_secretsmanager_secret.aurora_credentials.arn
}

output "security_group_id" {
  value = aws_security_group.aurora.id
}
