package config

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
)

func strPtr(s string) *string { return &s }

func TestGetClientHost(t *testing.T) {
	t.Run("falls back to host with docker rewrite", func(t *testing.T) {
		lk := &LivekitInfo{Host: "http://host.docker.internal:7880"}
		if got := lk.GetClientHost(); got != "http://localhost:7880" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("prefers client host", func(t *testing.T) {
		lk := &LivekitInfo{
			Host:       "http://host.docker.internal:7880",
			ClientHost: strPtr("https://livekit.example.com"),
		}
		if got := lk.GetClientHost(); got != "https://livekit.example.com" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestValidateLivekitInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   *string
		want    *string
		wantErr bool
	}{
		{"nil is allowed", nil, nil, false},
		{"empty becomes nil", strPtr("  "), nil, false},
		{"valid https", strPtr("https://livekit.example.com"), strPtr("https://livekit.example.com"), false},
		{"valid wss with path", strPtr("wss://livekit.example.com/rtc"), strPtr("wss://livekit.example.com/rtc"), false},
		{"trims surrounding spaces", strPtr("  http://10.0.0.5:7880  "), strPtr("http://10.0.0.5:7880"), false},
		{"missing scheme", strPtr("livekit.example.com"), nil, true},
		{"unsupported scheme", strPtr("ftp://livekit.example.com"), nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lk := &LivekitInfo{ClientHost: tt.input}
			err := validateLivekitInfo(lk)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if (lk.ClientHost == nil) != (tt.want == nil) {
				t.Fatalf("ClientHost = %v, want %v", lk.ClientHost, tt.want)
			}
			if lk.ClientHost != nil && *lk.ClientHost != *tt.want {
				t.Fatalf("ClientHost = %q, want %q", *lk.ClientHost, *tt.want)
			}
		})
	}
}

func TestValidateNatsInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   NatsInfo
		wantErr bool
	}{
		{"valid", NatsInfo{NatsUrls: []string{"nats://localhost:4222"}, NatsWSUrls: []string{"https://example.com/ws"}, NumReplicas: 1}, false},
		{"replicas unset", NatsInfo{NatsUrls: []string{"nats://localhost:4222"}}, false},
		{"missing nats_urls", NatsInfo{}, true},
		{"invalid nats url", NatsInfo{NatsUrls: []string{"localhost:4222"}}, true},
		{"invalid nats scheme", NatsInfo{NatsUrls: []string{"http://localhost:4222"}}, true},
		{"invalid ws url", NatsInfo{NatsUrls: []string{"nats://localhost:4222"}, NatsWSUrls: []string{"nats://example.com/ws"}}, true},
		{"invalid num_replicas", NatsInfo{NatsUrls: []string{"nats://localhost:4222"}, NumReplicas: 2}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNatsInfo(&tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRedisInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   connections.RedisInfo
		wantErr bool
	}{
		{"standalone valid", connections.RedisInfo{Host: "localhost:6379"}, false},
		{"standalone missing port", connections.RedisInfo{Host: "localhost"}, true},
		{"sentinel valid", connections.RedisInfo{MasterName: "mymaster", SentinelAddresses: []string{"host1:26379", "host2:26379"}}, false},
		{"sentinel missing master", connections.RedisInfo{SentinelAddresses: []string{"host1:26379"}}, true},
		{"sentinel invalid address", connections.RedisInfo{MasterName: "mymaster", SentinelAddresses: []string{"host1"}}, true},
		{"cluster valid", connections.RedisInfo{ClusterAddresses: []string{"localhost:7000", "localhost:7001"}}, false},
		{"cluster invalid address", connections.RedisInfo{ClusterAddresses: []string{"localhost"}}, true},
		{"cluster with sentinel", connections.RedisInfo{ClusterAddresses: []string{"localhost:7000"}, SentinelAddresses: []string{"host1:26379"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := connections.ValidateRedisInfo(&tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDatabaseInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   connections.DatabaseInfo
		wantErr bool
	}{
		{"valid", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet"}, false},
		{"missing host", connections.DatabaseInfo{Port: 3306, DBName: "plugnmeet"}, true},
		{"invalid port", connections.DatabaseInfo{Host: "localhost", Port: 0, DBName: "plugnmeet"}, true},
		{"port out of range", connections.DatabaseInfo{Host: "localhost", Port: 70000, DBName: "plugnmeet"}, true},
		{"missing db", connections.DatabaseInfo{Host: "localhost", Port: 3306}, true},
		{"replica inherits port", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet", Replicas: []connections.ReplicaDBInfo{{Host: "replica"}}}, false},
		{"replica missing host", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet", Replicas: []connections.ReplicaDBInfo{{Port: 3306}}}, true},
		{"replica invalid port", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet", Replicas: []connections.ReplicaDBInfo{{Host: "replica", Port: 70000}}}, true},
		{"failover valid", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet", Failover: &connections.FailoverSettings{Hosts: []connections.ReplicaDBInfo{{Host: "db2", Port: 3306}}}}, false},
		{"failover missing host", connections.DatabaseInfo{Host: "localhost", Port: 3306, DBName: "plugnmeet", Failover: &connections.FailoverSettings{Hosts: []connections.ReplicaDBInfo{{Port: 3306}}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := connections.ValidateDatabaseInfo(&tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
