output "server_ip" {
  description = "Public IPv4 address of the swarm server"
  value       = hcloud_server.swarm.ipv4_address
}

output "server_ipv6" {
  description = "Public IPv6 address of the swarm server"
  value       = hcloud_server.swarm.ipv6_address
}

output "ssh_command" {
  description = "SSH command to connect to the server"
  value       = "ssh root@${hcloud_server.swarm.ipv4_address}"
}

output "server_status" {
  description = "Current server status"
  value       = hcloud_server.swarm.status
}

output "gateway_url" {
  description = "OpenClaw gateway URL"
  value       = "ws://${hcloud_server.swarm.ipv4_address}:18789"
}
