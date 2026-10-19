output "bucket_name" {
  description = "Name of the S3 ads media bucket."
  value       = aws_s3_bucket.ads.id
}

output "bucket_arn" {
  description = "ARN of the S3 ads media bucket."
  value       = aws_s3_bucket.ads.arn
}

output "cloudfront_distribution_id" {
  description = "ID of the CloudFront distribution."
  value       = aws_cloudfront_distribution.ads.id
}

output "cloudfront_domain_name" {
  description = "Domain name of the CloudFront distribution (e.g. d111111abcdef8.cloudfront.net)."
  value       = aws_cloudfront_distribution.ads.domain_name
}

output "cloudfront_distribution_arn" {
  description = "ARN of the CloudFront distribution."
  value       = aws_cloudfront_distribution.ads.arn
}
