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
variable "debug_pause" {
  type    = bool
  default = false
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
variable "vnc_auth" {
  type    = string
  default = "vnc"
  validation {
    condition     = contains(["vnc", "apple"], var.vnc_auth)
    error_message = "The vnc_auth value must be vnc or apple."
  }
}

source "tart-cli" "test" {
  vm_base_name       = var.base_vm
  vm_name            = var.test_vm
  headless           = false
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
      "VNC_AUTH=${var.vnc_auth}",
    ]
    script  = "integration/configure-screen-sharing.sh"
    timeout = "2m"
  }

  provisioner "shell" {
    script  = "integration/prepare-terminal.sh"
    timeout = "2m"
  }

  provisioner "breakpoint" {
    disable = !var.debug_pause
    note    = "Before the VNC marker command: inspect Terminal and dismiss any modal dialog. Continue only when the fresh shell prompt has keyboard focus."
  }

  provisioner "vnc-commands" {
    host     = build.Host
    username = var.vnc_auth == "apple" ? var.ssh_username : ""
    password = var.vnc_auth == "apple" ? var.ssh_password : var.vnc_password
    # Screen Sharing's connection notification temporarily takes keyboard focus.
    # Wait after connecting, and let the last keys settle before disconnecting.
    commands        = ["<wait10s>", "printf '%s' 'vnc-commands-ok' > /tmp/vnc-commands-marker<enter>", "<wait2s>"]
    key_interval    = "100ms"
    connect_timeout = "30s"
    timeout         = "2m"

    preview        = true
    recording_path = "artifacts/${var.test_vm}.avi"
    frame_rate     = 5
  }

  provisioner "shell" {
    script  = "integration/verify-marker.sh"
    timeout = "1m"
  }

  provisioner "file" {
    source      = "integration/mouse-test.swift"
    destination = "/tmp/vnc-commands-mouse-test.swift"
  }

  provisioner "shell" {
    script  = "integration/prepare-mouse.sh"
    timeout = "3m"
  }

  provisioner "breakpoint" {
    disable = !var.debug_pause
    note    = "Before mouse commands: the VNC Mouse Test window should cover the desktop. Dismiss any modal dialog before continuing."
  }

  provisioner "vnc-commands" {
    host            = build.Host
    username        = var.vnc_auth == "apple" ? var.ssh_username : ""
    password        = var.vnc_auth == "apple" ? var.ssh_password : var.vnc_password
    commands        = ["<wait10s>", "<click 400,300><rightClick 75%,50%><click 25%,50%>", "<wait2s>"]
    key_interval    = "100ms"
    connect_timeout = "30s"
    timeout         = "1m"
    # Exercise resize tracking without requiring preview or recording.
  }

  provisioner "shell" {
    script  = "integration/verify-mouse.sh"
    timeout = "1m"
  }
}
