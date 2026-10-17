resource "aws_db_subnet_group" "this" {
  name       = "${var.cluster_name}-aurora"
  subnet_ids = var.private_subnet_ids
  tags       = var.tags
}

resource "aws_security_group" "aurora" {
  name        = "${var.cluster_name}-aurora-sg"
  description = "Aurora PostgreSQL security group"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [var.eks_node_security_group_id]
    description     = "PostgreSQL from EKS nodes"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = var.tags
}

resource "random_password" "aurora" {
  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret" "aurora_credentials" {
  name                    = "${var.cluster_name}/aurora/credentials"
  description             = "Aurora PostgreSQL master credentials"
  recovery_window_in_days = var.secret_recovery_window
  tags                    = var.tags
}

resource "aws_secretsmanager_secret_version" "aurora_credentials" {
  secret_id = aws_secretsmanager_secret.aurora_credentials.id
  secret_string = jsonencode({
    username = var.db_master_username
    password = random_password.aurora.result
    host     = var.enable_aurora_cluster ? aws_rds_cluster.this[0].endpoint : aws_db_instance.this[0].address
    port     = 5432
    dbname   = var.db_name
  })
}

# ---------------------------------------------------------
# Free-Tier Compatible Standard RDS PostgreSQL (Dev)
# ---------------------------------------------------------
resource "aws_db_instance" "this" {
  count = var.enable_aurora_cluster ? 0 : 1

  identifier             = "${var.cluster_name}-pg"
  engine                 = "postgres"
  engine_version         = "15"
  instance_class         = "db.t4g.micro"
  allocated_storage      = 20
  db_name                = var.db_name
  username               = var.db_master_username
  password               = random_password.aurora.result
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.aurora.id]
  skip_final_snapshot    = true
  deletion_protection    = false

  tags = var.tags
}

# ---------------------------------------------------------
# High-Availability Aurora PostgreSQL Cluster (Prod)
# ---------------------------------------------------------
resource "aws_rds_cluster" "this" {
  count = var.enable_aurora_cluster ? 1 : 0

  cluster_identifier     = "${var.cluster_name}-aurora"
  engine                 = "aurora-postgresql"
  engine_version         = var.engine_version
  database_name          = var.db_name
  master_username        = var.db_master_username
  master_password        = random_password.aurora.result
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.aurora.id]

  storage_encrypted            = true
  deletion_protection          = var.deletion_protection
  preferred_maintenance_window = "sun:04:00-sun:05:00"
  skip_final_snapshot          = var.skip_final_snapshot
  final_snapshot_identifier    = "${var.cluster_name}-aurora-final"

  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = var.tags
}

resource "aws_rds_cluster_instance" "this" {
  count = var.enable_aurora_cluster ? var.instance_count : 0

  identifier         = "${var.cluster_name}-aurora-${count.index}"
  cluster_identifier = aws_rds_cluster.this[0].id
  instance_class     = var.instance_class
  engine             = aws_rds_cluster.this[0].engine
  engine_version     = aws_rds_cluster.this[0].engine_version

  monitoring_interval                   = 60
  monitoring_role_arn                   = aws_iam_role.rds_monitoring[0].arn
  performance_insights_enabled          = true
  performance_insights_retention_period = 7

  tags = var.tags
}

resource "aws_iam_role" "rds_monitoring" {
  count = var.enable_aurora_cluster ? 1 : 0
  name  = "${var.cluster_name}-rds-monitoring"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "monitoring.rds.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count      = var.enable_aurora_cluster ? 1 : 0
  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}
