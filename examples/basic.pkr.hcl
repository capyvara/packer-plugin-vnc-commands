packer {
  required_plugins {
    vnc-commands = {
      source  = "github.com/capyvara/vnc-commands"
      version = "~> 0.1"
    }
  }
}

variable "vnc_host" {
  type    = string
  default = "127.0.0.1"
}

variable "vnc_password" {
  type      = string
  default   = ""
  sensitive = true
}

variable "vnc_username" {
  type    = string
  default = ""
}

# A validation-only example. Supply a real SSH/VNC guest before building.
source "null" "example" {
  ssh_host     = var.vnc_host
  ssh_username = "admin"
  ssh_password = "admin"
}

build {
  sources = ["source.null.example"]

  provisioner "shell" {
    inline = ["open -a Terminal"]
  }

  provisioner "vnc-commands" {
    host            = build.Host
    username        = var.vnc_username
    password        = var.vnc_password
    commands        = ["<wait2s>", "hello<enter>", "<wait2s>"]
    key_interval    = "100ms"
    connect_timeout = "30s"
    timeout         = "2m"
  }

  provisioner "shell" {
    inline = ["echo 'VNC provisioner completed'"]
  }
}
