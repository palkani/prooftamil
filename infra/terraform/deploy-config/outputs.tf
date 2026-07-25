output "secret_ids" {
  description = "Secret Manager secret ids created (bound by deploy.yml)."
  value       = sort(keys(google_secret_manager_secret.app))
}

output "runtime_service_account" {
  description = "SA granted read access to the secrets (Cloud Run runs as this)."
  value       = local.runtime_sa
}

output "github_repo" {
  description = "Repo that received GCP_SA_KEY / GCP_PROJECT / GCP_REGION."
  value       = "${var.github_owner}/${var.github_repo}"
}

# Deliberately NO output of secret values, the SA key, or the JWT private key.
