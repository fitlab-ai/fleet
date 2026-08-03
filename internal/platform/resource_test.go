package platform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fitlab-ai/fleet/internal/dataplane"
)

func TestParseLsofOwnerReturnsStableIdentity(t *testing.T) {
	owner, err := ParseLsofOwner("p4242\ncsing-box\na/opt/homebrew/bin/sing-box\na run\na-c\na/tmp/config.json\n")
	if err != nil {
		t.Fatal(err)
	}
	if owner.PID != 4242 || owner.Executable != "sing-box" || owner.ArgsFingerprint == "" {
		t.Fatalf("owner = %#v", owner)
	}
}

func TestPortOwnerCommandUsesEndpointNetwork(t *testing.T) {
	tcp := strings.Join(portOwnerCommand(dataplane.ListenEndpoint{Network: "tcp", Port: 7890}), " ")
	udp := strings.Join(portOwnerCommand(dataplane.ListenEndpoint{Network: "udp", Port: 7890}), " ")
	if tcp != "lsof -nP -iTCP:7890 -sTCP:LISTEN -Fpca" {
		t.Fatalf("TCP owner command = %q", tcp)
	}
	if udp != "lsof -nP -iUDP:7890 -Fpca" {
		t.Fatalf("UDP owner command = %q", udp)
	}
}

func TestFingerprintLinesIsOrderIndependent(t *testing.T) {
	first := FingerprintLines("route  one\nroute two\n")
	second := FingerprintLines(" route   two \nroute one\n")
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("fingerprints differ: %v vs %v", first, second)
	}
}

func TestFingerprintRouteLinesIgnoresExpiringNeighbors(t *testing.T) {
	before := `Routing tables

Internet:
Destination Gateway Flags Netif Expire
default 192.168.1.1 UGScg en0
192.168.1.1 48:bd:4a:f5:40:e2 UHLWIir en0 1197
172.19/30 172.19.0.1 USc utun8
`
	after := `Routing tables

Internet:
Destination Gateway Flags Netif Expire
default 192.168.1.1 UGScg en0
192.168.1.1 48:bd:4a:f5:40:e2 UHLWIir en0 1195
172.19/30 172.19.0.1 USc utun8
`
	if got, want := FingerprintRouteLines(after), FingerprintRouteLines(before); !reflect.DeepEqual(got, want) {
		t.Fatalf("route fingerprints differ after only expiry changed: %v vs %v", got, want)
	}
}

func TestFingerprintRouteLinesDetectsStableRouteChanges(t *testing.T) {
	before := "default 192.168.1.1 UGScg en0\n"
	after := before + "172.19/30 172.19.0.1 USc utun8\n"
	if reflect.DeepEqual(FingerprintRouteLines(after), FingerprintRouteLines(before)) {
		t.Fatal("stable TUN route change was ignored")
	}
}

func TestFingerprintRouteLinesAssociatesRoutesWithInterfaces(t *testing.T) {
	routes := FingerprintRouteLines("172.19/30 172.19.0.1 USc utun8\n")
	interfaces := FingerprintInterfaceNames("lo0 en0 utun8")
	utun := FingerprintInterfaceNames("utun8")[0]
	if len(routes) != 1 || !strings.HasPrefix(routes[0], utun+":") {
		t.Fatalf("route fingerprints = %v, want utun8 association", routes)
	}
	if len(interfaces) != 3 {
		t.Fatalf("interface fingerprints = %v, want one entry per interface", interfaces)
	}
}

func TestParseFullTunnelObservations(t *testing.T) {
	const routeHeader = "Destination Gateway Flags Netif Expire\n"
	tests := []struct {
		name    string
		ipv4    string
		ipv6    string
		want    []FullTunnelObservation
		wantErr bool
	}{
		{
			name: "default route on utun",
			ipv4: routeHeader + "default 10.0.0.1 UGSc utun7\n",
			want: []FullTunnelObservation{{Interface: "utun7", Family: "ipv4", Shape: "default"}},
		},
		{
			name: "split ipv4 and ipv6 defaults",
			ipv4: routeHeader + "0/1 172.19.0.1 UGSc utun9\n128/1 172.19.0.1 UGSc utun9\n",
			ipv6: routeHeader + "::/1 fe80::1 UGSc utun9\n8000::/1 fe80::1 UGSc utun9\n",
			want: []FullTunnelObservation{
				{Interface: "utun9", Family: "ipv4", Shape: "split"},
				{Interface: "utun9", Family: "ipv6", Shape: "split"},
			},
		},
		{
			name: "physical default and private utun are not full tunnel",
			ipv4: routeHeader + "default 192.168.1.1 UGSc en0\n10/8 10.0.0.1 UGSc utun4\n",
		},
		{
			name: "one split half is not a proven full tunnel",
			ipv4: routeHeader + "128.0/1 198.18.0.1 UGSc utun0\n",
		},
		{
			name: "macOS interface-scoped defaults are not full tunnels",
			ipv6: routeHeader + "default fe80::%utun1 UGcIg utun1\ndefault fe80::%utun2 UGcIg utun2\n",
		},
		{
			name:    "split halves on different tunnels are ambiguous",
			ipv4:    routeHeader + "0/1 10.0.0.1 UGSc utun7\n128/1 10.0.0.2 UGSc utun8\n",
			wantErr: true,
		},
		{
			name:    "unrecognized route output is ambiguous",
			ipv4:    "unexpected route output\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFullTunnelObservations(tt.ipv4, tt.ipv6)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("observations = %#v, want ambiguous route error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("observations = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestLegacyResourceFingerprintPreservesOpaqueV2Encoding(t *testing.T) {
	tunOutput := "lo0 en0 utun8\n"
	routeOutput := "default 192.168.1.1 UGScg en0\n172.19/30 172.19.0.1 USc utun8\n"
	for kind, output := range map[dataplane.ResourceKind]string{
		dataplane.ResourceTUN:   tunOutput,
		dataplane.ResourceRoute: routeOutput,
	} {
		if got, want := fingerprintResourceOutput(kind, output, true), FingerprintLines(output); !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy %s fingerprint = %v, want %v", kind, got, want)
		}
	}
	if reflect.DeepEqual(
		fingerprintResourceOutput(dataplane.ResourceTUN, tunOutput, true),
		fingerprintResourceOutput(dataplane.ResourceTUN, tunOutput, false),
	) {
		t.Fatal("legacy and ownership TUN encodings unexpectedly match")
	}
}

func TestNoopProxyNeverClaimsOwnership(t *testing.T) {
	proxy := NoopProxy{}
	owned, err := proxy.OwnedBy(t.Context(), "127.0.0.1", 7890)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("non-darwin noop proxy claimed system proxy ownership")
	}
}

var _ ResourceInspector = ExecResourceInspector{}
var _ = dataplane.ResourceDNS
