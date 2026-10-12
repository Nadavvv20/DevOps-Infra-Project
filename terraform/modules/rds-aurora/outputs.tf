output "cluster_endpoint" {
  value = aws_rds_cluster.this.endpoint
}

output "cluster_reader_endpoint" {
  value = aws_rds_cluster.this.reader_endpoint
}

output "cluster_identifier" {
  value = aws_rds_cluster.this.cluster_identifier
}

output "secret_arn" {
  value = aws_secretsmanager_secret.aurora_credentials.arn
}

output "security_group_id" {
  value = aws_security_group.aurora.id
}
