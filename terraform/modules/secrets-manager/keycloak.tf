# ─────────────────────────────────────────────────────────────────────────────
# Keycloak Admin Credentials — Secrets Manager
#
# Stores the bootstrap admin username/password used to first-time configure
# the Keycloak admin console. After bootstrap, create a service account in
# Keycloak and rotate this password.
# ─────────────────────────────────────────────────────────────────────────────

resource "random_password" "keycloak_admin" {
  length  = 24
  special = false
}

resource "aws_secretsmanager_secret" "keycloak_credentials" {
  name                    = "${var.cluster_name}/keycloak/credentials"
  description             = "Keycloak bootstrap admin credentials"
  recovery_window_in_days = var.secret_recovery_window
  tags                    = var.tags
}

resource "aws_secretsmanager_secret_version" "keycloak_credentials" {
  secret_id = aws_secretsmanager_secret.keycloak_credentials.id
  secret_string = jsonencode({
    admin_user     = "admin"
    admin_password = random_password.keycloak_admin.result
  })
}

output "keycloak_secret_arn" {
  description = "ARN of the Keycloak admin credentials secret."
  value       = aws_secretsmanager_secret.keycloak_credentials.arn
}
