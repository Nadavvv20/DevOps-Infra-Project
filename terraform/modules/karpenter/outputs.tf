output "irsa_arn" {
  value = module.karpenter_irsa.iam_role_arn
}

output "node_instance_profile_name" {
  value = aws_iam_instance_profile.karpenter.name
}

output "node_role_arn" {
  value = aws_iam_role.karpenter_node.arn
}

output "interruption_queue_name" {
  value = aws_sqs_queue.karpenter_interruption.name
}
