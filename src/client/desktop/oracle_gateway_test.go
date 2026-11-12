package main

import "testing"

func TestOracleListenHostPrefersAssignedProxy(t *testing.T) {
	got := oracleListenHost(false, "10.4.4.4:2222", "http://127.0.0.1:8080")
	if got != "10.4.4.4" {
		t.Fatal(got)
	}
}

func TestOracleListenHostIgnoresLoopbackAndAsset(t *testing.T) {
	if got := oracleListenHost(true, "10.8.8.8:2222", "http://127.0.0.1:8080"); got != oracleGatewayFallback {
		t.Fatal(got)
	}
	if got := oracleListenHost(false, "127.0.0.1:2222", "http://192.168.106.20:8080"); got != "192.168.106.20" {
		t.Fatal(got)
	}
	if got := oracleListenHost(true, "", "http://10.9.1.8:8080"); got != "10.9.1.8" {
		t.Fatal(got)
	}
}
