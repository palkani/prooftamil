# ── GCP Secret Manager: the 5 app secrets the deploy workflow binds by reference.
#
# Secret ids match deploy.yml's --set-secrets (gemini, db, redis, jwtpriv, jwtpub).
# NOTE: secret ids are project-global. If your v1 setup already has a secret with
# one of these exact names, either `terraform import` it or rename here.

data "google_project" "this" {
  project_id = var.project_id
}

locals {
  runtime_sa = coalesce(
    var.runtime_sa_email,
    "${data.google_project.this.number}-compute@developer.gserviceaccount.com",
  )
}

# JWT RS256 keypair — generated here; the private key lives only in Terraform state.
resource "tls_private_key" "jwt" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "google_project_service" "secretmanager" {
  project            = var.project_id
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

locals {
  secrets = {
    gemini  = var.gemini_api_key
    db      = var.database_url
    redis   = var.redis_url
    jwtpriv = tls_private_key.jwt.private_key_pem
    jwtpub  = tls_private_key.jwt.public_key_pem
  }
}

resource "google_secret_manager_secret" "app" {
  for_each   = local.secrets
  secret_id  = each.key
  labels     = { app = "prooftamil", managed_by = "terraform" }
  depends_on = [google_project_service.secretmanager]

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "app" {
  for_each    = local.secrets
  secret      = google_secret_manager_secret.app[each.key].id
  secret_data = each.value
}

# The Cloud Run runtime SA must be able to READ each secret at boot.
resource "google_secret_manager_secret_iam_member" "runtime_access" {
  for_each  = google_secret_manager_secret.app
  secret_id = each.value.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${local.runtime_sa}"
}
