output "repository_urls" {
  value = { for k, v in data.aws_ecr_repository.services : k => v.repository_url }
}

output "repository_arns" {
  value = { for k, v in data.aws_ecr_repository.services : k => v.arn }
}
