package network

import "fmt"

// amd64OnlyClients publish linux/amd64 images only. The k3d node runs the
// Docker host's architecture, so on any other host their pod never starts.
var amd64OnlyClients = map[string]bool{"silver": true}

func checkClientArch(client, arch string) error {
	if !amd64OnlyClients[client] || arch == "amd64" {
		return nil
	}

	return fmt.Errorf("consensus client %s publishes linux/amd64 images only, and this machine is %s.\n"+
		"Choose another --consensus-client: lighthouse, prysm, teku, nimbus, lodestar or grandine", client, arch)
}
