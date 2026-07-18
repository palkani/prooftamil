terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # Recommended: keep state in a GCS bucket, not on disk — it contains the SA key,
  # the JWT private key and the app secrets. Uncomment and point at your bucket.
  #
  # backend "gcs" {
  #   bucket = "prooftamil-tfstate"
  #   prefix = "deploy-config"
  # }
}
