resource "helm_release" "karpenter" {
  namespace        = "kube-system"
  name             = "karpenter"
  repository       = "oci://public.ecr.aws/karpenter"
  chart            = "karpenter"
  version          = var.karpenter_version
  create_namespace = false
  wait             = true

  values = [
    yamlencode({
      settings = {
        clusterName            = var.cluster_name
        clusterEndpoint        = var.cluster_endpoint
        interruptionQueue      = var.interruption_queue_name
      }
      serviceAccount = {
        annotations = {
          "eks.amazonaws.com/role-arn" = var.karpenter_irsa_arn
        }
      }
      controller = {
        resources = {
          requests = { cpu = "200m", memory = "256Mi" }
          limits   = { cpu = "1",    memory = "1Gi"   }
        }
      }
      tolerations = [{
        key      = "CriticalAddonsOnly"
        operator = "Exists"
      }]
    })
  ]
}

resource "kubectl_manifest" "karpenter_node_class" {
  yaml_body = yamlencode({
    apiVersion = "karpenter.k8s.aws/v1"
    kind       = "EC2NodeClass"
    metadata   = { name = "default" }
    spec = {
      amiSelectorTerms    = [{ alias = "al2023@latest" }]
      role                = split("/", var.karpenter_node_role_arn)[1]
      subnetSelectorTerms = [{ tags = { "karpenter.sh/discovery" = var.cluster_name } }]
      securityGroupSelectorTerms = [{ tags = { "karpenter.sh/discovery" = var.cluster_name } }]
      blockDeviceMappings = [{
        deviceName = "/dev/xvda"
        ebs = {
          volumeSize = "50Gi"
          volumeType = "gp3"
          encrypted  = true
          deleteOnTermination = true
        }
      }]
      userData = ""
      tags     = var.tags
    }
  })

  depends_on = [helm_release.karpenter]
}

resource "kubectl_manifest" "karpenter_node_pool" {
  yaml_body = yamlencode({
    apiVersion = "karpenter.sh/v1"
    kind       = "NodePool"
    metadata   = { name = "default" }
    spec = {
      template = {
        spec = {
          nodeClassRef = {
            group = "karpenter.k8s.aws"
            kind  = "EC2NodeClass"
            name  = "default"
          }
          requirements = [
            { key = "karpenter.sh/capacity-type",       operator = "In", values = ["spot", "on-demand"] },
            { key = "kubernetes.io/arch",               operator = "In", values = ["amd64"]             },
            { key = "karpenter.k8s.aws/instance-category", operator = "In", values = ["c", "m", "r"]   },
            { key = "karpenter.k8s.aws/instance-generation", operator = "Gt", values = ["2"]            },
          ]
        }
      }
      limits = {
        cpu    = "100"
        memory = "400Gi"
      }
      disruption = {
        consolidationPolicy = "WhenEmptyOrUnderutilized"
        consolidateAfter    = "1m"
      }
    }
  })

  depends_on = [kubectl_manifest.karpenter_node_class]
}

resource "helm_release" "aws_load_balancer_controller" {
  namespace        = "kube-system"
  name             = "aws-load-balancer-controller"
  repository       = "https://aws.github.io/eks-charts"
  chart            = "aws-load-balancer-controller"
  version          = var.alb_controller_version
  create_namespace = false
  wait             = true

  set {
    name  = "clusterName"
    value = var.cluster_name
  }

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = var.alb_controller_role_arn
  }

  set {
    name  = "region"
    value = var.aws_region
  }

  set {
    name  = "vpcId"
    value = var.vpc_id
  }
}

resource "helm_release" "external_secrets" {
  namespace        = "external-secrets"
  name             = "external-secrets"
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = var.external_secrets_version
  create_namespace = true
  wait             = true

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = var.external_secrets_role_arn
  }
}

resource "helm_release" "argocd" {
  namespace        = "argocd"
  name             = "argocd"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.argocd_version
  create_namespace = true
  wait             = true

  values = [
    yamlencode({
      global = {
        domain = var.argocd_hostname
      }
      server = {
        ingress = {
          enabled          = true
          ingressClassName = "alb"
          annotations = {
            "alb.ingress.kubernetes.io/scheme"       = "internet-facing"
            "alb.ingress.kubernetes.io/target-type"  = "ip"
            "alb.ingress.kubernetes.io/listen-ports" = "[{\"HTTPS\":443}]"
            "alb.ingress.kubernetes.io/certificate-arn" = var.acm_certificate_arn
          }
          hosts = [var.argocd_hostname]
        }
        service = { type = "ClusterIP" }
      }
      configs = {
        params = {
          "server.insecure" = true
        }
      }
    })
  ]
}

resource "helm_release" "kube_prometheus_stack" {
  namespace        = "monitoring"
  name             = "kube-prometheus-stack"
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  version          = var.prometheus_stack_version
  create_namespace = true
  wait             = false

  values = [
    yamlencode({
      prometheus = {
        prometheusSpec = {
          retention      = "30d"
          storageSpec = {
            volumeClaimTemplate = {
              spec = {
                storageClassName = "gp3"
                resources        = { requests = { storage = "50Gi" } }
              }
            }
          }
          serviceMonitorSelectorNilUsesHelmValues = false
          podMonitorSelectorNilUsesHelmValues     = false
        }
      }
      grafana = {
        enabled          = true
        adminPassword    = var.grafana_admin_password
        persistence = {
          enabled          = true
          storageClassName = "gp3"
          size             = "10Gi"
        }
        ingress = {
          enabled          = true
          ingressClassName = "alb"
          annotations = {
            "alb.ingress.kubernetes.io/scheme"      = "internet-facing"
            "alb.ingress.kubernetes.io/target-type" = "ip"
          }
          hosts = [var.grafana_hostname]
        }
      }
      alertmanager = {
        enabled = true
        alertmanagerSpec = {
          storage = {
            volumeClaimTemplate = {
              spec = {
                storageClassName = "gp3"
                resources        = { requests = { storage = "5Gi" } }
              }
            }
          }
        }
      }
    })
  ]
}
