variable "hcloud_token" {
  description = "Hetzner Cloud API token"
  type        = string
  sensitive   = true
}

variable "environment" {
  description = "Environment name (dev, prod)"
  type        = string
  default     = "dev"
}

variable "server_type" {
  description = "Hetzner server type (cx22 = 2 vCPU, 4GB RAM, ~5 EUR/mo)"
  type        = string
  default     = "cx22"
}

variable "location" {
  description = "Hetzner datacenter location (fsn1 = Falkenstein, nbg1 = Nuremberg)"
  type        = string
  default     = "fsn1"
}

variable "ssh_public_key_path" {
  description = "Path to SSH public key for server access"
  type        = string
  default     = "~/.ssh/id_ed25519.pub"
}

variable "openrouter_api_key" {
  description = "OpenRouter API key for LLM access"
  type        = string
  sensitive   = true
}

variable "telegram_bot_token" {
  description = "Telegram bot token for Kira"
  type        = string
  sensitive   = true
  default     = ""
}

variable "telegram_chat_id" {
  description = "Telegram chat ID for the founders group"
  type        = string
  default     = ""
}

variable "swarm_repo_url" {
  description = "Git URL for the swarm repo (cloned on the server)"
  type        = string
  default     = "https://github.com/RaaSaaR-org/swarm.git"
}
