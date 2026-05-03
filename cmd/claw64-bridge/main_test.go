package main

import "testing"

func TestNormalizeCLISerialPortDisablesSpawnVICE(t *testing.T) {
	cli := CLI{SerialPort: "/dev/cu.C64", SpawnVICE: true}
	normalizeCLI(&cli)
	if cli.SpawnVICE {
		t.Fatalf("SpawnVICE = true, want false when SerialPort is set")
	}
}

func TestNormalizeCLILeavesSpawnVICEWithoutSerialPort(t *testing.T) {
	cli := CLI{SpawnVICE: true}
	normalizeCLI(&cli)
	if !cli.SpawnVICE {
		t.Fatalf("SpawnVICE = false, want unchanged true when SerialPort is empty")
	}
}
