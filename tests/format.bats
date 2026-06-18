#!/usr/bin/env bats
# unit formatting: durations, memory, docker ages, path truncation

load helper

setup() { load_fn fmt_up fmt_mem fmt_runfor fish_path trunc pad; }

@test "fmt_up: compact durations" {
  [ "$(fmt_up 42)" = "42s" ]
  [ "$(fmt_up 2520)" = "42m" ]
  [ "$(fmt_up 60000)" = "16h" ]
  [ "$(fmt_up 108000)" = "1d6h" ]
}

@test "fmt_mem: docker MemUsage to short units" {
  [ "$(fmt_mem 1.137MiB)" = "1.1M" ]
  [ "$(fmt_mem 28.3MiB)" = "28M" ]
  [ "$(fmt_mem 9.715MiB)" = "9.7M" ]
  [ "$(fmt_mem 1.2GiB)" = "1.2G" ]
}

@test "fmt_runfor: docker RunningFor to compact" {
  [ "$(fmt_runfor '2 hours ago')" = "2h" ]
  [ "$(fmt_runfor '2 days')" = "2d" ]
  [ "$(fmt_runfor 'About a minute ago')" = "1m" ]
  [ "$(fmt_runfor 'About an hour')" = "1h" ]
}

@test "fish_path: abbreviates the path but keeps the tail" {
  HOME=/home/u
  [ "$(fish_path /home/u/code/checkout-service 22)" = "~/c/checkout-service" ]
  # too narrow: the END survives, never the head
  out=$(fish_path /tmp/cc-daemon/2026/job-xyz 10)
  [[ "$out" == …* ]]
  [[ "$out" == *job-xyz ]]
}

@test "pad: pads by display chars so multibyte glyphs align" {
  out=$(pad "respawn ×3" 12)
  [ "${#out}" -eq 12 ]   # bash counts chars, like the terminal does
}
