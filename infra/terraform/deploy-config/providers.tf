# google  → authenticate with Application Default Credentials:
#             gcloud auth application-default login
# github  → reads the GITHUB_TOKEN env var (a token with repo admin scope):
#             export GITHUB_TOKEN="$(gh auth token)"

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "github" {
  owner = var.github_owner
  # token comes from $GITHUB_TOKEN
}
