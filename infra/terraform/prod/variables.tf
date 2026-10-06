variable "region" {
  type    = string
  default = "us-east-1"
}

variable "release" {
  description = "Commit SHA being deployed; artifacts live at s3://<artifact_bucket>/<release>/<function>.zip"
  type        = string
}

variable "artifact_bucket" { type = string }

variable "domain_name" {
  description = "The API's custom domain (Conventions → Base URL)"
  type        = string
  default     = "api.substratalapps.com"
}

variable "hosted_zone" {
  description = "Route 53 hosted zone that domain_name and mail_domain live in"
  type        = string
  default     = "substratalapps.com"
}

variable "mail_domain" {
  description = "SES sending domain (DKIM/SPF/DMARC)"
  type        = string
  default     = "mail.substratalapps.com"
}

variable "dashboard_url" {
  type    = string
  default = "https://dashboard.substratalapps.com"
}

variable "authorize_url" {
  type    = string
  default = "https://auth.substratalapps.com/authorize"
}

variable "cors_origins" {
  description = "Comma-separated first-party browser origins (NFR → CORS)"
  type        = string
  default     = "https://dashboard.substratalapps.com"
}

variable "alarm_email" {
  description = "Where operational alarms go (empty: create the topic only)"
  type        = string
  default     = ""
}

variable "db_min_acu" {
  description = "Aurora Serverless v2 floor; 0.5 is the ~$43/month Tier 0 floor"
  type        = number
  default     = 0.5
}

variable "db_max_acu" {
  description = "Aurora Serverless v2 ceiling, set deliberately (no surprise scale-out)"
  type        = number
  default     = 4
}

variable "api_throttle_rate" {
  description = "API Gateway steady-state requests/second, a cost guard above the app's own rate limits"
  type        = number
  default     = 200
}
