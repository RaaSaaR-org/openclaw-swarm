# VPS module — reusable server provisioning
# Use this module to create additional dedicated servers per customer (if needed)

variable "name" {
  description = "Server name"
  type        = string
}

variable "server_type" {
  description = "Hetzner server type"
  type        = string
  default     = "cx22"
}

variable "location" {
  description = "Datacenter location"
  type        = string
  default     = "fsn1"
}

variable "ssh_key_ids" {
  description = "List of SSH key IDs"
  type        = list(string)
}

variable "firewall_ids" {
  description = "List of firewall IDs"
  type        = list(string)
  default     = []
}

variable "user_data" {
  description = "Cloud-init user data"
  type        = string
  default     = ""
}

variable "labels" {
  description = "Server labels"
  type        = map(string)
  default     = {}
}

resource "hcloud_server" "this" {
  name        = var.name
  image       = "ubuntu-24.04"
  server_type = var.server_type
  location    = var.location
  ssh_keys    = var.ssh_key_ids
  firewall_ids = var.firewall_ids
  user_data   = var.user_data
  labels      = var.labels
}

output "ipv4_address" {
  value = hcloud_server.this.ipv4_address
}

output "ipv6_address" {
  value = hcloud_server.this.ipv6_address
}

output "id" {
  value = hcloud_server.this.id
}
