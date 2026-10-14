variable "project_name" {
  type    = string
  default = "online-boutique"
}

variable "service_names" {
  type = list(string)
  default = [
    "frontend",
    "cartservice",
    "productcatalogservice",
    "currencyservice",
    "paymentservice",
    "checkoutservice",
    "emailservice",
    "recommendationservice",
    "shippingservice",
    "adservice",
    "loadgenerator",
    "inventoryservice",
  ]
}

variable "tags" {
  type    = map(string)
  default = {}
}
