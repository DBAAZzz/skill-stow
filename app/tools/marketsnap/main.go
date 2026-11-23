// marketsnap 从 skills.sh 公开网页提取技能简介与专题目录，生成供客户端离线兜底与补全的 market-extras.json。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var opts runOptions
	flag.StringVar(&opts.baseURL, "base-url", defaultSkillsShBase, "skills.sh base URL")
	flag.StringVar(&opts.prevPath, "prev", "", "optional path to previous market-extras.json")
	flag.StringVar(&opts.prevURL, "prev-url", "", "optional URL of previous market-extras.json (ignored when -prev is set)")
	flag.StringVar(&opts.outPath, "out", "market-extras.json", "output JSON file path (or - for stdout)")
	flag.StringVar(&opts.userAgent, "user-agent", defaultUserAgent, "HTTP User-Agent header (must not contain personal email)")
	flag.IntVar(&opts.limit, "limit", 0, "max skill pages to fetch in this run (0 = unlimited)")
	flag.IntVar(&opts.concurrency, "concurrency", defaultConcurrency, "max concurrent HTTP requests")
	flag.DurationVar(&opts.interval, "interval", defaultInterval, "minimum spacing between HTTP requests")
	flag.IntVar(&opts.maxRetries, "max-retries", defaultMaxRetries, "retry attempts on 429/5xx or network error")
	flag.DurationVar(&opts.timeout, "timeout", defaultTimeout, "per-request HTTP timeout")
	flag.IntVar(&opts.maxBytes, "max-bytes", defaultMaxBytes, "max allowed output JSON size in bytes")
	flag.IntVar(&opts.minSkills, "min-skills", 1, "minimum required descriptions count in output")
	flag.IntVar(&opts.rotateDays, "rotate-days", defaultRotateDays, "rotate cached descriptions every N days (1/N per day)")
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "marketsnap: unexpected arguments: %v\n", flag.Args())
		os.Exit(2)
	}

	stats, err := runSnapshot(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marketsnap: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "marketsnap: ok in %s (candidates=%d topics=%d descriptions=%d fetched=%d reused=%d failed=%d bytes=%d out=%s)\n",
		stats.Duration, stats.CandidateSkills, stats.Topics, stats.Descriptions, stats.Fetched, stats.Reused, stats.FetchFailed, stats.Bytes, opts.outPath)
}
