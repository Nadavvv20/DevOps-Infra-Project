# ─────────────────────────────────────────────────────────────────────────────
# IAM Roles for Service Accounts (IRSA) — Online Boutique
#
# Uses the AWS-maintained terraform-aws-modules/iam module for IRSA.
# Each role gets a trust policy scoped to exactly ONE Kubernetes
# ServiceAccount in exactly ONE namespace — zero-privilege-escalation.
# ─────────────────────────────────────────────────────────────────────────────

# ── External Secrets Operator ─────────────────────────────────────────────────
module "external_secrets_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name                             = "${var.cluster_name}-external-secrets"
  attach_external_secrets_policy        = true
  external_secrets_secrets_manager_arns = var.secrets_manager_arns

  oidc_providers = {
    main = {
      provider_arn               = var.oidc_provider_arn
      namespace_service_accounts = ["external-secrets:external-secrets-sa"]
    }
  }

  tags = var.tags
}

# ── AWS Load Balancer Controller ──────────────────────────────────────────────
module "alb_controller_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name                              = "${var.cluster_name}-aws-load-balancer-controller"
  attach_load_balancer_controller_policy = true

  oidc_providers = {
    main = {
      provider_arn               = var.oidc_provider_arn
      namespace_service_accounts = ["kube-system:aws-load-balancer-controller"]
    }
  }

  tags = var.tags
}

# ── CloudWatch Agent ──────────────────────────────────────────────────────────
module "cloudwatch_agent_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name                              = "${var.cluster_name}-cloudwatch-agent"
  attach_cloudwatch_observability_policy = true

  oidc_providers = {
    main = {
      provider_arn               = var.oidc_provider_arn
      namespace_service_accounts = ["amazon-cloudwatch:cloudwatch-agent"]
    }
  }

  tags = var.tags
}

# ── SNS Producer — checkoutservice ───────────────────────────────────────────
# checkoutservice publishes OrderPlaced events to the SNS topic after ACID commit.
# It only needs sns:Publish on the specific SNS topic.
resource "aws_iam_role" "checkoutservice_sqs" {
  name = "${var.cluster_name}-checkoutservice-sqs"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.oidc_provider_arn }
      Condition = {
        StringEquals = {
          "${var.oidc_issuer_url}:sub" = "system:serviceaccount:online-boutique:checkoutservice"
          "${var.oidc_issuer_url}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  force_detach_policies = true
  tags                  = var.tags
}

resource "aws_iam_role_policy" "checkoutservice_sqs" {
  name = "sns-publish-order-events"
  role = aws_iam_role.checkoutservice_sqs.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sns:Publish"]
      Resource = [var.order_events_topic_arn]
    }]
  })
}

# ── SQS Consumer — emailservice ───────────────────────────────────────────────
# emailservice polls the email queue and DLQ only. CANNOT access shipping queue.
resource "aws_iam_role" "emailservice_sqs" {
  name = "${var.cluster_name}-emailservice-sqs"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.oidc_provider_arn }
      Condition = {
        StringEquals = {
          "${var.oidc_issuer_url}:sub" = "system:serviceaccount:online-boutique:emailservice"
          "${var.oidc_issuer_url}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  force_detach_policies = true
  tags                  = var.tags
}

resource "aws_iam_role_policy" "emailservice_sqs" {
  name = "sqs-consume-email-messages"
  role = aws_iam_role.emailservice_sqs.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "sqs:ReceiveMessage",
        "sqs:DeleteMessage",
        "sqs:GetQueueAttributes",
        "sqs:ChangeMessageVisibility"
      ]
      Resource = [var.email_queue_arn, var.email_dlq_arn]
    }]
  })
}

# ── SQS Consumer — shippingservice ───────────────────────────────────────────
# shippingservice polls the shipping queue and DLQ only. CANNOT access email queue.
resource "aws_iam_role" "shippingservice_sqs" {
  name = "${var.cluster_name}-shippingservice-sqs"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.oidc_provider_arn }
      Condition = {
        StringEquals = {
          "${var.oidc_issuer_url}:sub" = "system:serviceaccount:online-boutique:shippingservice"
          "${var.oidc_issuer_url}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  force_detach_policies = true
  tags                  = var.tags
}

resource "aws_iam_role_policy" "shippingservice_sqs" {
  name = "sqs-consume-shipping-messages"
  role = aws_iam_role.shippingservice_sqs.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "sqs:ReceiveMessage",
        "sqs:DeleteMessage",
        "sqs:GetQueueAttributes",
        "sqs:ChangeMessageVisibility"
      ]
      Resource = [var.shipping_queue_arn, var.shipping_dlq_arn]
    }]
  })
}

# ── YACE CloudWatch Exporter ──────────────────────────────────────────────────
# yace-sqs-exporter requires read-only CloudWatch metric permissions.
resource "aws_iam_role" "yace_sqs_exporter" {
  name = "${var.cluster_name}-yace-sqs-exporter"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.oidc_provider_arn }
      Condition = {
        StringEquals = {
          "${var.oidc_issuer_url}:sub" = "system:serviceaccount:monitoring:yace-sqs-exporter"
          "${var.oidc_issuer_url}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  force_detach_policies = true
  tags                  = var.tags
}

resource "aws_iam_role_policy" "yace_sqs_exporter" {
  name = "cloudwatch-metrics-read"
  role = aws_iam_role.yace_sqs_exporter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "tag:GetResources",
        "cloudwatch:GetMetricData",
        "cloudwatch:ListMetrics"
      ]
      Resource = ["*"]
    }]
  })
}

