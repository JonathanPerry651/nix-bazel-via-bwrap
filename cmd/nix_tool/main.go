package main

import (
	"flag"
	"log"
	"os"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/cache"
)

func main() {
	src := flag.String("src", "", "Source NAR archive path")
	dest := flag.String("dest", "", "Destination directory")
	compression := flag.String("compression", "xz", "Compression type (xz, bzip2, none)")
	flag.Parse()

	if *src == "" || *dest == "" {
		flag.Usage()
		os.Exit(1)
	}

	f, err := os.Open(*src)
	if err != nil {
		log.Fatalf("Failed to open source: %v", err)
	}
	defer f.Close()

	if err := cache.UnpackNar(f, *compression, *dest); err != nil {
		log.Fatalf("Failed to unpack NAR: %v", err)
	}
}
