package server

import "testing"

func TestChooseDeploymentPortStartsFromDeploymentID(t *testing.T) {
	port, err := chooseDeploymentPort(3, map[int]bool{}, 20000, 20010)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20002 {
		t.Fatalf("expected port 20002, got %d", port)
	}
}

func TestChooseDeploymentPortSkipsUsedPorts(t *testing.T) {
	port, err := chooseDeploymentPort(1, map[int]bool{20000: true, 20001: true}, 20000, 20003)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20002 {
		t.Fatalf("expected port 20002, got %d", port)
	}
}

func TestChooseDeploymentPortWraps(t *testing.T) {
	port, err := chooseDeploymentPort(4, map[int]bool{20002: true}, 20000, 20002)
	if err != nil {
		t.Fatal(err)
	}
	if port != 20000 {
		t.Fatalf("expected port 20000, got %d", port)
	}
}

func TestChooseDeploymentPortFull(t *testing.T) {
	_, err := chooseDeploymentPort(1, map[int]bool{20000: true, 20001: true}, 20000, 20001)
	if err == nil {
		t.Fatal("expected error for full port range")
	}
}
