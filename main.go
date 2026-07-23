package main

import (
	"cliutils/hostexpansion"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "hosts" {
		fmt.Fprintln(os.Stderr, "usage: cliutils hosts \"host[1..2]\"")
		os.Exit(2)
	}
	for _, host := range hostexpansion.ExpandHosts(os.Args[2]) {
		fmt.Println(host)
	}
}
