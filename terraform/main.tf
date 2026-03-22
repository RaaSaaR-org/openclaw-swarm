terraform {
  required_version = ">= 1.5"

  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.45"
    }
  }
}

provider "hcloud" {
  token = var.hcloud_token
}

# SSH key for server access
resource "hcloud_ssh_key" "deploy" {
  name       = "emai-swarm-${var.environment}"
  public_key = file(var.ssh_public_key_path)
}

# Firewall: only allow SSH + gateway ports
resource "hcloud_firewall" "swarm" {
  name = "emai-swarm-${var.environment}"

  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "18789"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  # HTTPS for future web dashboard
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

# Server with cloud-init to install Docker + OpenClaw
resource "hcloud_server" "swarm" {
  name        = "emai-swarm-${var.environment}"
  image       = "ubuntu-24.04"
  server_type = var.server_type
  location    = var.location
  ssh_keys    = [hcloud_ssh_key.deploy.id]

  firewall_ids = [hcloud_firewall.swarm.id]

  user_data = templatefile("${path.module}/cloud-init.yml", {
    openrouter_api_key = var.openrouter_api_key
    telegram_bot_token = var.telegram_bot_token
    telegram_chat_id   = var.telegram_chat_id
    swarm_repo_url     = var.swarm_repo_url
  })

  labels = {
    project     = "emai"
    component   = "swarm"
    environment = var.environment
  }
}
