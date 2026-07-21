#!/usr/bin/env bash

_cloud_ssh_run_attempt() {
  local stage="$1"
  shift

  local deadline_epoch="${WK_CLOUD_SSH_DEADLINE_EPOCH:-}"
  if [[ -z "$deadline_epoch" ]]; then
    "$@"
    return $?
  fi
  if [[ ! "$deadline_epoch" =~ ^[0-9]+$ ]]; then
    printf 'cloud-ssh stage=%s result=failed status=2 retryable=false reason=invalid-deadline\n' "$stage" >&2
    return 2
  fi

  local now_epoch
  if now_epoch="$(date -u +%s)"; then
    :
  else
    local status=$?
    printf 'cloud-ssh stage=%s result=failed status=%d retryable=false reason=deadline-clock\n' \
      "$stage" "$status" >&2
    return "$status"
  fi
  local remaining_seconds=$((deadline_epoch - now_epoch))
  if ((remaining_seconds <= 0)); then
    return 124
  fi

  local timeout_command
  if timeout_command="$(command -v timeout)"; then
    :
  else
    printf 'cloud-ssh stage=%s result=failed status=127 retryable=false reason=timeout-unavailable\n' "$stage" >&2
    return 127
  fi
  "$timeout_command" --foreground --signal=TERM --kill-after=10s "${remaining_seconds}s" "$@"
}

_cloud_ssh_remove_capture() {
  local stage="$1"
  local output_file="$2"
  if rm -f "$output_file"; then
    return 0
  else
    local status=$?
    printf 'cloud-ssh stage=%s result=failed status=%d retryable=false reason=capture-cleanup\n' \
      "$stage" "$status" >&2
    return "$status"
  fi
}

# cloud_ssh_retry runs one SSH-family command with a bounded retry budget.
# The stage label is intentionally restricted because it is written to CI logs.
cloud_ssh_retry() {
  if (($# < 4)); then
    printf 'cloud_ssh_retry requires STAGE ATTEMPTS DELAY_SECONDS COMMAND...\n' >&2
    return 2
  fi

  local stage="$1"
  local attempts="$2"
  local delay_seconds="$3"
  shift 3

  if [[ ! "$stage" =~ ^[A-Za-z0-9._:-]+$ ]] ||
    [[ ! "$attempts" =~ ^[0-9]+$ ]] || ((attempts < 1 || attempts > 30)) ||
    [[ ! "$delay_seconds" =~ ^[0-9]+$ ]] || ((delay_seconds > 60)); then
    printf 'cloud_ssh_retry received an invalid bounded retry policy\n' >&2
    return 2
  fi

  local attempt
  local status=1
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    printf 'cloud-ssh stage=%s attempt=%d/%d\n' "$stage" "$attempt" "$attempts" >&2
    if _cloud_ssh_run_attempt "$stage" "$@"; then
      printf 'cloud-ssh stage=%s attempt=%d/%d result=success\n' "$stage" "$attempt" "$attempts" >&2
      return 0
    else
      status=$?
    fi
    if ((status != 255)); then
      if ((status == 124)); then
        printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=124 retryable=false reason=deadline-exceeded\n' \
          "$stage" "$attempt" "$attempts" >&2
      else
        printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=%d retryable=false\n' \
          "$stage" "$attempt" "$attempts" "$status" >&2
      fi
      return "$status"
    fi
    if ((attempt < attempts)); then
      sleep "$delay_seconds"
    fi
  done

  printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=%d retryable=true\n' \
    "$stage" "$attempts" "$attempts" "$status" >&2
  return "$status"
}

# cloud_ssh_retry_capture emits stdout only from the successful attempt. It is
# intended for read-only SSH probes whose captured value feeds a later command.
cloud_ssh_retry_capture() {
  if (($# < 4)); then
    printf 'cloud_ssh_retry_capture requires STAGE ATTEMPTS DELAY_SECONDS COMMAND...\n' >&2
    return 2
  fi

  local stage="$1"
  local attempts="$2"
  local delay_seconds="$3"
  shift 3

  if [[ ! "$stage" =~ ^[A-Za-z0-9._:-]+$ ]] ||
    [[ ! "$attempts" =~ ^[0-9]+$ ]] || ((attempts < 1 || attempts > 30)) ||
    [[ ! "$delay_seconds" =~ ^[0-9]+$ ]] || ((delay_seconds > 60)); then
    printf 'cloud_ssh_retry_capture received an invalid bounded retry policy\n' >&2
    return 2
  fi

  local temporary_root="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
  local output_file=""
  if output_file="$(mktemp "${temporary_root%/}/cloud-ssh-capture.XXXXXX")"; then
    :
  else
    local status=$?
    printf 'cloud-ssh stage=%s result=failed status=%d retryable=false reason=capture-create\n' \
      "$stage" "$status" >&2
    return "$status"
  fi
  if chmod 0600 "$output_file"; then
    :
  else
    local status=$?
    _cloud_ssh_remove_capture "$stage" "$output_file" || true
    printf 'cloud-ssh stage=%s result=failed status=%d retryable=false reason=capture-permission\n' \
      "$stage" "$status" >&2
    return "$status"
  fi

  local attempt
  local status=1
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if : >"$output_file"; then
      :
    else
      status=$?
      _cloud_ssh_remove_capture "$stage" "$output_file" || true
      printf 'cloud-ssh stage=%s result=failed status=%d retryable=false reason=capture-reset\n' \
        "$stage" "$status" >&2
      return "$status"
    fi
    printf 'cloud-ssh stage=%s attempt=%d/%d\n' "$stage" "$attempt" "$attempts" >&2
    if _cloud_ssh_run_attempt "$stage" "$@" >"$output_file"; then
      if command cat "$output_file"; then
        :
      else
        status=$?
        _cloud_ssh_remove_capture "$stage" "$output_file" || true
        printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=%d retryable=false reason=capture-output\n' \
          "$stage" "$attempt" "$attempts" "$status" >&2
        return "$status"
      fi
      if _cloud_ssh_remove_capture "$stage" "$output_file"; then
        :
      else
        status=$?
        return "$status"
      fi
      printf 'cloud-ssh stage=%s attempt=%d/%d result=success\n' "$stage" "$attempt" "$attempts" >&2
      return 0
    else
      status=$?
    fi
    if ((status != 255)); then
      if _cloud_ssh_remove_capture "$stage" "$output_file"; then
        :
      else
        local cleanup_status=$?
        return "$cleanup_status"
      fi
      if ((status == 124)); then
        printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=124 retryable=false reason=deadline-exceeded\n' \
          "$stage" "$attempt" "$attempts" >&2
      else
        printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=%d retryable=false\n' \
          "$stage" "$attempt" "$attempts" "$status" >&2
      fi
      return "$status"
    fi
    if ((attempt < attempts)); then
      sleep "$delay_seconds"
    fi
  done

  if _cloud_ssh_remove_capture "$stage" "$output_file"; then
    :
  else
    local cleanup_status=$?
    return "$cleanup_status"
  fi
  printf 'cloud-ssh stage=%s attempt=%d/%d result=failed status=%d retryable=true\n' \
    "$stage" "$attempts" "$attempts" "$status" >&2
  return "$status"
}
