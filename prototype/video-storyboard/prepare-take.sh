#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
task_dir="$script_dir/demo-task"
fixture="$task_dir/fixtures/token.go.failing"
target="$task_dir/token.go"

cp "$fixture" "$target"

if ! command -v claude >/dev/null 2>&1; then
	printf '%s\n' "Claude Code is not on PATH. Install it before recording." >&2
	exit 1
fi

if ! command -v claude-companion >/dev/null 2>&1; then
	printf '%s\n' "Claude Companion is not on PATH. Install or link it before recording." >&2
	exit 1
fi

log_file=$(mktemp)
trap 'rm -f "$log_file"' EXIT HUP INT TERM

if (cd "$task_dir" && go test ./... >"$log_file" 2>&1); then
	printf '%s\n' "The demo unexpectedly passes. The failing fixture was not restored." >&2
	exit 1
fi

if ! grep -q "expired_token" "$log_file"; then
	printf '%s\n' "The demo failed somewhere unexpected:" >&2
	cat "$log_file" >&2
	exit 1
fi

printf '%s\n\n' "Demo reset: the expired-token case fails as intended."
printf '%s\n' "cd $task_dir"
printf '%s\n\n' "claude --dangerously-load-development-channels server:claude-companion"
printf '%s\n' "Opening prompt:"
printf '%s\n' "Fix the failing expired-token test. Change only the validator, then run exactly go test ./... once. Don't run any other checks."
