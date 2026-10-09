package main

import (
	"fmt"
	"os"

	"github.com/capyvara/packer-plugin-vnc-commands/provisioner/vnccommands"
	"github.com/hashicorp/packer-plugin-sdk/plugin"
	"github.com/hashicorp/packer-plugin-sdk/version"
)

// Set by GoReleaser; local builds remain distinguishable from releases.
var pluginVersion = "0.2.0-dev"

func main() {
	set := plugin.NewSet()
	set.RegisterProvisioner(plugin.DEFAULT_NAME, new(vnccommands.Provisioner))
	set.SetVersion(version.NewRawVersion(pluginVersion))
	if err := set.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
