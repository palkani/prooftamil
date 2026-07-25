# ── GitHub Actions config for the v2 repo, so the deploy workflow can run.

# Mint a fresh JSON key for the EXISTING deploy service account. A service account
# can hold several keys; v1's key keeps working. (GitHub secrets are write-only, so
# the value cannot be copied out of v1 — it has to come from GCP.)
data "google_service_account" "deployer" {
  account_id = var.deployer_sa_email
  project    = var.project_id
}

resource "google_service_account_key" "deployer" {
  service_account_id = data.google_service_account.deployer.name
}

resource "github_actions_secret" "gcp_sa_key" {
  repository  = var.github_repo
  secret_name = "GCP_SA_KEY"
  # google exports the key base64-encoded; the workflow wants the raw JSON.
  plaintext_value = base64decode(google_service_account_key.deployer.private_key)
}

resource "github_actions_variable" "gcp_project" {
  repository    = var.github_repo
  variable_name = "GCP_PROJECT"
  value         = var.project_id
}

resource "github_actions_variable" "gcp_region" {
  repository    = var.github_repo
  variable_name = "GCP_REGION"
  value         = var.region
}
