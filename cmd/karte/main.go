package main

import (
    "flag"
    "fmt"
    "log"
    "os"

    "karte/internal/runner"
)

func main() {
    if len(os.Args) < 2 {
        usage()
        os.Exit(1)
    }
    switch os.Args[1] {
    case "init":
        cmdInit()
    case "serve":
        cmdServe()
    case "build":
        cmdBuild()
    default:
        usage()
        os.Exit(1)
    }
}

func usage() {
    fmt.Println("karte <init|serve|build> [-p port]")
}

func cmdInit() {
    if err := runner.InitProject("."); err != nil {
        log.Fatal(err)
    }
    fmt.Println("Initialized Karte project.")
}

func cmdServe() {
    fs := flag.NewFlagSet("serve", flag.ExitOnError)
    port := fs.Int("p", 1313, "port")
    _ = fs.Parse(os.Args[2:])
    if err := runner.Serve(".", *port); err != nil {
        log.Fatal(err)
    }
}

func cmdBuild() {
    if err := runner.Build("."); err != nil {
        log.Fatal(err)
    }
}
