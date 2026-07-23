#!/usr/bin/env bash

# This file is sourced by local Cloud Simulation entrypoints. Reset private
# ownership state on source so inherited environment variables can never make
# cleanup remove a caller-owned directory.
unset WK_LOCAL_TOOL_SHIM_DIR WK_LOCAL_TOOL_SHIM_OWNED WK_LOCAL_TOOL_ORIGINAL_PATH
unset WK_LOCAL_TOOL_ORIGINAL_GOROOT WK_LOCAL_TOOL_ORIGINAL_GOROOT_SET
unset WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN_SET
unset WK_RUN_BOUNDED_EXEC_WRAPPER WK_BOUNDED_WRAPPER_PID
WK_LOCAL_TOOL_SHIM_DIR=""
WK_LOCAL_TOOL_SHIM_OWNED=false
WK_LOCAL_TOOL_ORIGINAL_PATH=""
WK_LOCAL_TOOL_ORIGINAL_GOROOT=""
WK_LOCAL_TOOL_ORIGINAL_GOROOT_SET=false
WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN=""
WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN_SET=false

WK_LOCAL_RUNTIME_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WK_LOCAL_RUNTIME_TOOLCHAIN_FILE="${WK_CLOUD_TOOLCHAIN_FILE:-$WK_LOCAL_RUNTIME_DIR/../../.github/cloud-sim/toolchain.env}"
WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS="${WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS:-30}"
WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS="${WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS:-180}"

# wk_load_local_cloud_toolchain reads the repository pin without executing the
# manifest. Only strict numeric semantic versions are accepted.
wk_load_local_cloud_toolchain() {
  local go_version=""
  local gh_version=""
  [[ -f "$WK_LOCAL_RUNTIME_TOOLCHAIN_FILE" ]] || {
    printf 'cloud-sim local runtime: required toolchain manifest is missing: %s\n' \
      "$WK_LOCAL_RUNTIME_TOOLCHAIN_FILE" >&2
    return 1
  }
  go_version="$(awk -F= '$1 == "GO_VERSION" { print $2 }' "$WK_LOCAL_RUNTIME_TOOLCHAIN_FILE")"
  gh_version="$(awk -F= '$1 == "GH_CLI_VERSION" { print $2 }' "$WK_LOCAL_RUNTIME_TOOLCHAIN_FILE")"
  if [[ ! "$go_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ||
    ! "$gh_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf 'cloud-sim local runtime: invalid Go/GitHub CLI pins in %s\n' \
      "$WK_LOCAL_RUNTIME_TOOLCHAIN_FILE" >&2
    return 1
  fi
  if [[ ! "$WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ||
    ! "$WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' 'cloud-sim local runtime: command timeout overrides must be positive integer seconds' >&2
    return 1
  fi
  WK_EXPECTED_GO_VERSION="$go_version"
  WK_EXPECTED_GH_CLI_VERSION="$gh_version"
  export WK_EXPECTED_GO_VERSION WK_EXPECTED_GH_CLI_VERSION
}

# wk_resolve_executable selects an explicit override, the first fixed platform
# candidate, or finally the caller PATH. Overrides must be absolute executable
# paths; fixed candidates keep non-interactive automation deterministic.
wk_resolve_executable() {
  local command_name="$1"
  local override_name="$2"
  shift 2

  local override_value="${!override_name:-}"
  local candidate=""
  if [[ -n "$override_value" ]]; then
    if [[ "$override_value" != /* || ! -x "$override_value" ]]; then
      printf 'cloud-sim local runtime: %s must be an absolute executable path: %s\n' \
        "$override_name" "$override_value" >&2
      return 1
    fi
    printf '%s\n' "$override_value"
    return 0
  fi

  for candidate in "$@"; do
    [[ -n "$candidate" && -x "$candidate" ]] || continue
    printf '%s\n' "$candidate"
    return 0
  done
  candidate="$(command -v "$command_name" 2>/dev/null || true)"
  if [[ -n "$candidate" && -x "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi
  return 1
}

# wk_run_bounded owns one isolated process group, enforces a wall-clock limit,
# and returns 124 whenever that deadline fires. Perl is already a Cloud
# Analysis prerequisite and provides the same process-group semantics on macOS
# and Linux, unlike the platform-specific timeout/setsid commands.
wk_run_bounded() {
  local timeout_seconds="$1"
  shift
  local perl_bin="/usr/bin/perl"
  local ignore_signals="${WK_RUN_BOUNDED_IGNORE_SIGNALS:-false}"
  local lc_all_set=false
  local lang_set=false
  local lc_all_value="${LC_ALL:-}"
  local lang_value="${LANG:-}"
  [[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || return 125
  (($# > 0)) || return 125
  [[ "$ignore_signals" == true || "$ignore_signals" == false ]] || return 125
  if [[ ! -x "$perl_bin" ]]; then
    perl_bin="$(command -v perl 2>/dev/null || true)"
  fi
  [[ -n "$perl_bin" && -x "$perl_bin" ]] || {
    printf '%s\n' 'cloud-sim local runtime: perl is required for bounded process-group execution' >&2
    return 127
  }
  [[ -z "${LC_ALL+x}" ]] || lc_all_set=true
  [[ -z "${LANG+x}" ]] || lang_set=true

  local perl_program='
    use strict;
    use warnings;
    my ($timeout, $ignore_signals, $lc_all_set, $lc_all, $lang_set, $lang, @command) = @ARGV;
    exit 125 unless $timeout =~ /^[1-9][0-9]*$/ && @command;
    my $pid = fork();
    exit 125 unless defined $pid;
    if ($pid == 0) {
      POSIX::setpgid(0, 0) or exit 126;
      if ($lc_all_set eq "true") { $ENV{LC_ALL} = $lc_all; } else { delete $ENV{LC_ALL}; }
      if ($lang_set eq "true") { $ENV{LANG} = $lang; } else { delete $ENV{LANG}; }
      exec { $command[0] } @command;
      exit 127;
    }
    # Establish the group from the parent too. This closes the scheduler race
    # where the deadline could fire before the child gets its first timeslice.
    if (!POSIX::setpgid($pid, $pid) && $! != EACCES && $! != ESRCH) {
      kill "KILL", $pid;
      waitpid($pid, 0);
      exit 125;
    }
    my $deadline_fired = 0;
    my $forwarded_signal = 0;
    my $terminate_group = sub {
      kill "TERM", -$pid;
      select undef, undef, undef, 0.25;
      kill "KILL", -$pid;
    };
    local $SIG{ALRM} = sub { $deadline_fired = 1; $terminate_group->(); };
    if ($ignore_signals eq "true") {
      $SIG{HUP} = "IGNORE";
      $SIG{INT} = "IGNORE";
      $SIG{QUIT} = "IGNORE";
      $SIG{TERM} = "IGNORE";
    } else {
      $SIG{HUP} = sub { $forwarded_signal = 1; $terminate_group->(); };
      $SIG{INT} = sub { $forwarded_signal = 2; $terminate_group->(); };
      $SIG{QUIT} = sub { $forwarded_signal = 3; $terminate_group->(); };
      $SIG{TERM} = sub { $forwarded_signal = 15; $terminate_group->(); };
    }
    alarm $timeout;
    while (waitpid($pid, 0) < 0) {
      next if $! == EINTR;
      alarm 0;
      exit 125;
    }
    my $status = $?;
    alarm 0;
    # A child that outlives its direct parent is still owned by this bounded
    # operation. Kill the now-orphaned remainder before returning.
    kill "KILL", -$pid;
    exit 124 if $deadline_fired;
    exit 128 + $forwarded_signal if $forwarded_signal;
    exit 128 + ($status & 127) if $status & 127;
    exit($status >> 8);
  '
  if [[ "${WK_RUN_BOUNDED_EXEC_WRAPPER:-false}" == true ]]; then
    LC_ALL=C
    LANG=C
    export LC_ALL LANG
    exec "$perl_bin" -MPOSIX -MErrno=EINTR,EACCES,ESRCH -e "$perl_program" \
      "$timeout_seconds" "$ignore_signals" "$lc_all_set" "$lc_all_value" "$lang_set" "$lang_value" "$@"
  fi
  LC_ALL=C LANG=C "$perl_bin" -MPOSIX -MErrno=EINTR,EACCES,ESRCH -e "$perl_program" \
    "$timeout_seconds" "$ignore_signals" "$lc_all_set" "$lc_all_value" "$lang_set" "$lang_value" "$@"
}

# wk_start_bounded launches the same process-group wrapper asynchronously and
# exposes its actual Perl wrapper PID. Sending TERM to that PID makes the
# wrapper terminate and reap the complete child process group.
wk_start_bounded() {
  WK_BOUNDED_WRAPPER_PID=""
  (
    WK_RUN_BOUNDED_EXEC_WRAPPER=true wk_run_bounded "$@"
  ) &
  WK_BOUNDED_WRAPPER_PID=$!
  export WK_BOUNDED_WRAPPER_PID
}

wk_stop_bounded() {
  local wrapper_pid="$1"
  [[ "$wrapper_pid" =~ ^[1-9][0-9]*$ ]] || return 125
  kill -TERM "$wrapper_pid" 2>/dev/null || true
}

# wk_gh applies the shared per-command bound to ordinary non-interactive GitHub
# CLI calls. Workflow polling adds a separate whole-operation deadline.
wk_gh() {
  wk_run_bounded "$WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS" gh "$@"
}

# wk_cleanup_local_cloud_tools removes only a shim directory marked as owned
# by this process and restores the caller's original PATH.
wk_cleanup_local_cloud_tools() {
  local owner=""
  if [[ "${WK_LOCAL_TOOL_SHIM_OWNED:-false}" == true &&
    -n "${WK_LOCAL_TOOL_SHIM_DIR:-}" &&
    -f "$WK_LOCAL_TOOL_SHIM_DIR/.owner" ]]; then
    IFS= read -r owner <"$WK_LOCAL_TOOL_SHIM_DIR/.owner" || owner=""
    if [[ "$owner" == "$$" ]]; then
      rm -rf -- "$WK_LOCAL_TOOL_SHIM_DIR"
    else
      printf 'cloud-sim local runtime: refusing to remove an unowned tool shim directory: %s\n' \
        "$WK_LOCAL_TOOL_SHIM_DIR" >&2
    fi
  fi
  if [[ "${WK_LOCAL_TOOL_SHIM_OWNED:-false}" == true &&
    -n "${WK_LOCAL_TOOL_ORIGINAL_PATH:-}" ]]; then
    PATH="$WK_LOCAL_TOOL_ORIGINAL_PATH"
    export PATH
  fi
  if [[ "${WK_LOCAL_TOOL_SHIM_OWNED:-false}" == true ]]; then
    if [[ "${WK_LOCAL_TOOL_ORIGINAL_GOROOT_SET:-false}" == true ]]; then
      GOROOT="$WK_LOCAL_TOOL_ORIGINAL_GOROOT"
      export GOROOT
    else
      unset GOROOT
    fi
    if [[ "${WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN_SET:-false}" == true ]]; then
      GOTOOLCHAIN="$WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN"
      export GOTOOLCHAIN
    else
      unset GOTOOLCHAIN
    fi
  fi
  WK_LOCAL_TOOL_SHIM_DIR=""
  WK_LOCAL_TOOL_SHIM_OWNED=false
  WK_LOCAL_TOOL_ORIGINAL_PATH=""
  WK_LOCAL_TOOL_ORIGINAL_GOROOT=""
  WK_LOCAL_TOOL_ORIGINAL_GOROOT_SET=false
  WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN=""
  WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN_SET=false
}

# wk_select_exact_go resolves the pinned toolchain through a candidate launcher,
# then returns the executable inside that exact GOROOT. GOTOOLCHAIN=local on the
# resolved binary prevents subsequent commands from silently switching again.
wk_select_exact_go() {
  local launcher="$1"
  local expected="go$2"
  local selected_root=""
  local selected_go=""
  local actual=""

  selected_root="$(wk_run_bounded "$WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS" \
    env -u GOROOT GOWORK=off GOTOOLCHAIN="$expected" \
    "$launcher" env GOROOT 2>/dev/null)" || return 1
  selected_go="${selected_root%/}/bin/go"
  [[ "$selected_root" == /* && -x "$selected_go" ]] || return 1
  actual="$(wk_run_bounded "$WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS" \
    env -u GOROOT GOWORK=off GOTOOLCHAIN=local \
    "$selected_go" env GOVERSION 2>/dev/null)" || return 1
  [[ "$actual" == "$expected" ]] || return 1
  printf '%s\n%s\n' "$selected_go" "$selected_root"
}

# wk_verify_exact_gh rejects a stale PATH or explicit override instead of
# silently running a different GitHub CLI than the repository manifest pins.
wk_verify_exact_gh() {
  local executable="$1"
  local expected="$2"
  local version_output=""
  local actual=""
  version_output="$(wk_run_bounded "$WK_LOCAL_TOOL_COMMAND_TIMEOUT_SECONDS" \
    "$executable" --version 2>/dev/null)" || return 1
  actual="$(awk 'NR == 1 && $1 == "gh" && $2 == "version" { print $3 }' <<<"$version_output")"
  [[ "$actual" == "$expected" ]]
}

# wk_resolve_pinned_gh returns the deterministic executable only after its
# version matches the repository toolchain contract.
wk_resolve_pinned_gh() {
  local gh_path=""
  gh_path="$(wk_resolve_executable gh WK_GH_BIN \
    /opt/homebrew/bin/gh \
    /usr/local/bin/gh \
    /usr/bin/gh)" || {
      printf '%s\n' 'cloud-sim local runtime: gh is required' >&2
      return 1
    }
  if ! wk_verify_exact_gh "$gh_path" "$WK_EXPECTED_GH_CLI_VERSION"; then
    printf 'cloud-sim local runtime: GitHub CLI %s is required; selected executable is not an exact match: %s\n' \
      "$WK_EXPECTED_GH_CLI_VERSION" "$gh_path" >&2
    return 1
  fi
  printf '%s\n' "$gh_path"
}

# wk_activate_local_cloud_tools installs process-owned shims. go_path/go_root
# may be empty for cleanup-only entrypoints which must remain usable without Go.
wk_activate_local_cloud_tools() {
  local gh_path="$1"
  local go_path="${2:-}"
  local go_root="${3:-}"
  local shim_dir=""

  wk_cleanup_local_cloud_tools
  WK_LOCAL_TOOL_ORIGINAL_PATH="$PATH"
  if [[ -n "${GOROOT+x}" ]]; then
    WK_LOCAL_TOOL_ORIGINAL_GOROOT="$GOROOT"
    WK_LOCAL_TOOL_ORIGINAL_GOROOT_SET=true
  fi
  if [[ -n "${GOTOOLCHAIN+x}" ]]; then
    WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN="$GOTOOLCHAIN"
    WK_LOCAL_TOOL_ORIGINAL_GOTOOLCHAIN_SET=true
  fi
  shim_dir="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-cloud-tools.XXXXXX")" || {
    printf '%s\n' 'cloud-sim local runtime: cannot create the private tool shim directory' >&2
    return 1
  }
  chmod 0700 "$shim_dir"
  printf '%s\n' "$$" >"$shim_dir/.owner"
  if ! ln -s "$gh_path" "$shim_dir/gh"; then
    rm -rf "$shim_dir"
    printf '%s\n' 'cloud-sim local runtime: cannot activate the selected GitHub CLI executable' >&2
    return 1
  fi
  if [[ -n "$go_path" ]] && ! ln -s "$go_path" "$shim_dir/go"; then
    rm -rf "$shim_dir"
    printf '%s\n' 'cloud-sim local runtime: cannot activate the selected Go executable' >&2
    return 1
  fi
  WK_LOCAL_TOOL_SHIM_DIR="$shim_dir"
  WK_LOCAL_TOOL_SHIM_OWNED=true
  export WK_LOCAL_TOOL_SHIM_DIR
  if [[ -n "$go_root" ]]; then
    GOROOT="$go_root"
    GOTOOLCHAIN=local
    export GOROOT GOTOOLCHAIN
  fi
  PATH="${shim_dir}${PATH:+:${PATH}}"
  export PATH
}

# wk_resolve_local_github_tool prepares only gh. Cleanup and billing release
# must not be blocked merely because the local Go toolchain is unavailable.
wk_resolve_local_github_tool() {
  local gh_path=""
  wk_load_local_cloud_toolchain || return 1
  gh_path="$(wk_resolve_pinned_gh)" || return 1
  WK_RESOLVED_GH_BIN="$gh_path"
  export WK_RESOLVED_GH_BIN
  wk_activate_local_cloud_tools "$gh_path"
}

# wk_resolve_local_cloud_tools makes the exact gh and Go selected by preflight
# available to this script and to isolated subprocesses through private shims.
# The shim directory prevents an earlier stale PATH entry from winning and also
# supports explicit override executables whose basenames are not gh or go.
wk_resolve_local_cloud_tools() {
  local gh_path=""
  local go_launcher=""
  local go_selection=""
  local go_path=""
  local go_root=""
  local goroot_go=""
  if [[ -n "${GOROOT:-}" ]]; then
    goroot_go="${GOROOT%/}/bin/go"
  fi

  wk_load_local_cloud_toolchain || return 1

  gh_path="$(wk_resolve_pinned_gh)" || return 1
  go_launcher="$(wk_resolve_executable go WK_GO_BIN \
    /usr/local/go/bin/go \
    /opt/homebrew/bin/go \
    /opt/homebrew/opt/go/libexec/bin/go \
    /usr/local/bin/go \
    "$goroot_go")" || {
      printf '%s\n' 'cloud-sim local runtime: go is required' >&2
      return 1
    }
  go_selection="$(wk_select_exact_go "$go_launcher" "$WK_EXPECTED_GO_VERSION")" || {
    printf 'cloud-sim local runtime: Go %s is required and could not be selected from launcher: %s\n' \
      "$WK_EXPECTED_GO_VERSION" "$go_launcher" >&2
    return 1
  }
  go_path="${go_selection%%$'\n'*}"
  go_root="${go_selection#*$'\n'}"
  if [[ "$go_path" != /* || ! -x "$go_path" || "$go_root" != /* || "$go_root" == "$go_selection" ]]; then
    printf '%s\n' 'cloud-sim local runtime: exact Go selection returned an invalid executable/root pair' >&2
    return 1
  fi

  WK_RESOLVED_GH_BIN="$gh_path"
  WK_RESOLVED_GO_BIN="$go_path"
  WK_RESOLVED_GO_ROOT="$go_root"
  export WK_RESOLVED_GH_BIN WK_RESOLVED_GO_BIN WK_RESOLVED_GO_ROOT

  wk_activate_local_cloud_tools "$gh_path" "$go_path" "$go_root"
}

# wk_find_correlated_workflow_run locates only the exact run-name correlation
# produced by one request_id. Transient GitHub list failures remain bounded.
wk_find_correlated_workflow_run() {
  local repository="$1"
  local workflow="$2"
  local expected_title="$3"
  local runs=""
  local candidate=""
  local attempt=0
  local deadline=$((SECONDS + 120))
  local remaining=0
  local command_timeout=0

  for ((attempt = 0; attempt < 30 && SECONDS < deadline; attempt += 1)); do
    remaining=$((deadline - SECONDS))
    command_timeout="$WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS"
    ((command_timeout <= remaining)) || command_timeout="$remaining"
    if runs="$(wk_run_bounded "$command_timeout" gh run list --workflow "$workflow" --repo "$repository" \
      --event workflow_dispatch --branch main --limit 20 --json databaseId,displayTitle 2>/dev/null)"; then
      candidate="$(jq -r --arg title "$expected_title" \
        '[.[] | select(.displayTitle == $title)] | sort_by(.databaseId) | reverse | .[0].databaseId // empty' \
        <<<"$runs" 2>/dev/null || true)"
      if [[ "$candidate" =~ ^[0-9]+$ && "$candidate" != 0 ]]; then
        printf '%s\n' "$candidate"
        return 0
      fi
    fi
    ((SECONDS >= deadline)) || sleep 2
  done
  return 1
}

# wk_join_workflow_run polls the exact workflow directly so both GitHub
# transport failures and workflow queue time remain bounded by this function.
wk_join_workflow_run() {
  local repository="$1"
  local workflow_run="$2"
  local label="$3"
  local run_json=""
  local status=""
  local conclusion=""
  local attempt=0
  local deadline=$((SECONDS + 1800))
  local remaining=0
  local command_timeout=0

  # Thirty minutes covers the longest cleanup job timeout plus ordinary queue
  # delay while keeping the entire local wait strictly bounded.
  for ((attempt = 0; attempt < 360 && SECONDS < deadline; attempt += 1)); do
    remaining=$((deadline - SECONDS))
    command_timeout="$WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS"
    ((command_timeout <= remaining)) || command_timeout="$remaining"
    if run_json="$(wk_run_bounded "$command_timeout" gh run view "$workflow_run" --repo "$repository" \
      --json status,conclusion 2>/dev/null)"; then
      status="$(jq -r '.status // empty' <<<"$run_json" 2>/dev/null || true)"
      conclusion="$(jq -r '.conclusion // empty' <<<"$run_json" 2>/dev/null || true)"
      if [[ "$status" == completed ]]; then
        if [[ "$conclusion" == success ]]; then
          printf '%s workflow %s completed successfully.\n' \
            "$label" "$workflow_run" >&2
          return 0
        fi
        [[ -n "$conclusion" ]] || conclusion=unknown
        printf '%s workflow %s completed with conclusion %s.\n' \
          "$label" "$workflow_run" "$conclusion" >&2
        return 2
      fi
    fi
    if ((attempt < 359 && SECONDS < deadline)); then
      sleep 5
    fi
  done
  printf 'Cannot confirm a terminal state for %s workflow %s within the bounded recovery window.\n' \
    "$label" "$workflow_run" >&2
  return 3
}

# wk_dispatch_and_join_workflow tolerates the ambiguous case where GitHub
# accepted a dispatch but the local gh process returned an error. It dispatches
# exactly once, then locates and joins only the exact request correlation.
wk_dispatch_and_join_workflow() {
  local repository="$1"
  local workflow="$2"
  local expected_title="$3"
  local label="$4"
  shift 4
  local -a dispatch_arguments=("$@")
  local dispatch_status=0
  local workflow_run=""

  wk_run_bounded "$WK_LOCAL_GH_COMMAND_TIMEOUT_SECONDS" \
    gh workflow run "$workflow" --repo "$repository" --ref main \
    "${dispatch_arguments[@]}" >&2 || dispatch_status=$?
  if ! workflow_run="$(wk_find_correlated_workflow_run "$repository" "$workflow" "$expected_title")"; then
    printf '%s workflow was not visible after the single dispatch (gh exit %d); refusing to redispatch an ambiguous request correlation.\n' \
      "$label" "$dispatch_status" >&2
    return 1
  elif ((dispatch_status != 0)); then
    printf '%s dispatch returned gh exit %d, but the exact correlated workflow %s exists; joining it.\n' \
      "$label" "$dispatch_status" "$workflow_run" >&2
  fi

  local join_status=0
  if wk_join_workflow_run "$repository" "$workflow_run" "$label"; then
    :
  else
    join_status=$?
    return "$join_status"
  fi
  printf '%s\n' "$workflow_run"
}
