package rawproxy

import "context"

// Acik surumde kume (cok dugum) yoktur: ham TCP/UDP akisi yalnizca bu dugumdeki
// ajan oturumundan acilir. Ozel surumdeki cluster_dial.go'nun (dugumler arasi tunel)
// yerine gecen en kucuk karsiliklar; Manager.Remote her zaman nil'dir.

// RemoteDialer, acik surumde kullanilmaz (Manager.Remote nil kalir).
type RemoteDialer struct{}

// remoteStreams, Remote nil oldugu icin uretilmez; derleme uyumu icin yerel Hub'a duser.
type remoteStreams struct {
	local  hubStreams
	remote *RemoteDialer
}

func (s remoteStreams) online(clientID string) bool { return s.local.online(clientID) }

func (s remoteStreams) open(ctx context.Context, ti TunnelInfo, clientID, proto, remote string) (bridgeStream, bool, error) {
	return s.local.open(ctx, ti, clientID, proto, remote)
}

func (m *Manager) remoteOnline(string) bool { return false }

func (m *Manager) useRemote(string) bool { return false }
