# DNS module — optional customer subdomain management
# Requires a Hetzner DNS zone. Skip if DNS is managed elsewhere.

variable "zone_id" {
  description = "Hetzner DNS zone ID"
  type        = string
}

variable "subdomain" {
  description = "Subdomain name (e.g., 'customer-a' for customer-a.swarm.example.com)"
  type        = string
}

variable "target_ip" {
  description = "Target IPv4 address"
  type        = string
}

variable "ttl" {
  description = "DNS record TTL in seconds"
  type        = number
  default     = 300
}

resource "hcloud_dns_record" "a" {
  zone_id = var.zone_id
  name    = var.subdomain
  type    = "A"
  value   = var.target_ip
  ttl     = var.ttl
}

output "fqdn" {
  value = "${var.subdomain}.${data.hcloud_dns_zone.this.name}"
}

data "hcloud_dns_zone" "this" {
  id = var.zone_id
}
