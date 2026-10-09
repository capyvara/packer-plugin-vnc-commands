packer {
  required_plugins {
    tart = {
      source  = "github.com/cirruslabs/tart"
      version = "= 1.21.0"
    }
    vnc-commands = {
      source  = "github.com/capyvara/vnc-commands"
      version = "~> 0.1"
    }
  }
}

variable "base_vm" {
  type    = string
  default = "macos-base"
}
variable "test_vm" {
  type = string
}
variable "ssh_username" {
  type    = string
  default = "admin"
}
variable "ssh_password" {
  type      = string
  default   = "admin"
  sensitive = true
}
variable "vnc_password" {
  type      = string
  default   = "vnctest"
  sensitive = true
}

source "tart-cli" "test" {
  vm_base_name       = var.base_vm
  vm_name            = var.test_vm
  headless           = true
  disable_vnc        = true
  recovery_partition = "keep"
  cpu_count          = 4
  memory_gb          = 8
  ssh_username       = var.ssh_username
  ssh_password       = var.ssh_password
  ssh_timeout        = "3m"
}

build {
  sources = ["source.tart-cli.test"]
  provisioner "shell" {
    environment_vars = [
      "MACHINE_PASSWORD=${var.ssh_password}",
      "VNC_PASSWORD=${var.vnc_password}",
    ]
    script  = "integration/prepare-terminal.sh"
    timeout = "2m"
  }

  provisioner "vnc-commands" {
    host            = build.Host
    password        = var.vnc_password
    commands        = ["<wait2s>", "printf '%s' 'vnc-commands-ok' > /tmp/vnc-commands-marker<enter>"]
    key_interval    = "30ms"
    connect_timeout = "30s"
    timeout         = "2m"
  }

  provisioner "shell" {
    script  = "integration/verify-marker.sh"
    timeout = "1m"
  }
}
