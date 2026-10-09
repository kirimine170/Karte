package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"karte/internal/worklog"
	"os"
	"os/signal"
	"time"
)

func run(args []string, in io.Reader, out, diagnostic io.Writer) int {
	f := flag.NewFlagSet("karte-worklog", flag.ContinueOnError)
	f.SetOutput(diagnostic)
	root := f.String("root", "", "explicit Karte data root")
	observer := f.Bool("observer", false, "host/controller observations (not available for serve)")
	actor := f.String("actor", "ephy-worklog", "init actor")
	project := f.String("project", "ephy-worklog", "init project")
	zone := f.String("timezone", "Asia/Tokyo", "init timezone")
	sensitivity := f.String("sensitivity", "internal", "init sensitivity")
	date := f.String("date", "", "daily receipt date, default today in configured timezone")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if *root == "" || f.NArg() != 1 {
		fmt.Fprintln(diagnostic, "Usage: karte-worklog --root DATA_DIR [options] init|start|append|read|search|daily|serve. start/append/read/search read JSON from stdin.")
		return 2
	}
	command := f.Arg(0)
	if command == "init" {
		err := worklog.Initialize(*root, worklog.Config{Version: worklog.Version, Enabled: true, Actor: *actor, Project: *project, Timezone: *zone, Sensitivity: *sensitivity})
		if err != nil {
			fmt.Fprintln(diagnostic, err)
			return 1
		}
		json.NewEncoder(out).Encode(map[string]any{"configured": true})
		return 0
	}
	origin := "model_report"
	if *observer {
		origin = "host_observation"
	}
	s, err := worklog.New(*root, origin)
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	if command == "serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := s.Serve(ctx, in, out); err != nil {
			fmt.Fprintln(diagnostic, err)
			return 1
		}
		return 0
	}
	var value any
	if command == "daily" {
		if *date == "" {
			configBytes, e := os.ReadFile(*root + "/.mdsys/worklog/v1/config.json")
			if e != nil {
				fmt.Fprintln(diagnostic, e)
				return 1
			}
			var c worklog.Config
			if e = worklog.Decode(configBytes, &c); e != nil {
				fmt.Fprintln(diagnostic, e)
				return 1
			}
			loc, e := time.LoadLocation(c.Timezone)
			if e != nil {
				return 1
			}
			*date = time.Now().In(loc).Format("2006-01-02")
		}
		value, err = s.Daily(*date)
	} else {
		raw, e := io.ReadAll(io.LimitReader(in, (2<<20)+1))
		if e != nil {
			fmt.Fprintln(diagnostic, e)
			return 1
		}
		switch command {
		case "start", "append", "read", "search":
			value, err = s.Call("karte_work_"+command, raw)
		default:
			fmt.Fprintln(diagnostic, "unknown command")
			return 2
		}
	}
	payload := map[string]any{"value": value}
	if err != nil {
		payload["error"] = err.Error()
	}
	if e := json.NewEncoder(out).Encode(payload); e != nil {
		fmt.Fprintln(diagnostic, e)
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
