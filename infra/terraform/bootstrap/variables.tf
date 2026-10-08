variable "region" {
  type    = string
  default = "us-east-1"
}

variable "github_repository" {
  description = "owner/name of the repository allowed to assume the CI roles"
  type        = string
  default     = "CompositeCode/substratalapps.com"
}

variable "billing_email" {
  description = "Where budget, anomaly, and billing-alarm alerts go"
  type        = string
}

variable "warn_budget_usd" {
  description = "Warn budget: alerts at 50/80/100%, sized just above the ~$45–55 Tier 0 floor"
  type        = number
  default     = 75
}

variable "hard_budget_usd" {
  description = "Hard budget: the circuit-breaker action fires at 100%"
  type        = number
  default     = 150
}
