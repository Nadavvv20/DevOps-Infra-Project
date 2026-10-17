data "aws_ecr_repository" "services" {
  for_each = toset(var.service_names)

  name = "${var.project_name}/${each.value}"
}
