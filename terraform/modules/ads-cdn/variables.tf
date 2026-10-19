variable "cluster_name" {
  type        = string
  description = "Unique cluster identifier used as a prefix for all resource names."
}

variable "default_root_object" {
  type        = string
  default     = "ads.json"
  description = "Default root object served by CloudFront."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to assign to resources."
}
