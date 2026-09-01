# Validate the complete changelog and optionally print one exact release body.
# Usage: awk -v version=v3.0.0-beta.5 -f scripts/extract-release-notes.awk CHANGELOG.md

function fail(message) {
  print "CHANGELOG.md:" NR ": " message > "/dev/stderr"
  failed = 1
  exit 1
}

function is_semver(tag, core, prerelease, dash, count, parts, identifiers, i) {
  if (substr(tag, 1, 1) != "v") {
    return 0
  }
  tag = substr(tag, 2)
  dash = index(tag, "-")
  if (dash > 0) {
    core = substr(tag, 1, dash - 1)
    prerelease = substr(tag, dash + 1)
    if (prerelease == "") {
      return 0
    }
  } else {
    core = tag
    prerelease = ""
  }

  count = split(core, parts, ".")
  if (count != 3) {
    return 0
  }
  for (i = 1; i <= count; i++) {
    if (parts[i] !~ /^[0-9]+$/ || (length(parts[i]) > 1 && substr(parts[i], 1, 1) == "0")) {
      return 0
    }
  }

  if (prerelease != "") {
    count = split(prerelease, identifiers, ".")
    for (i = 1; i <= count; i++) {
      if (identifiers[i] == "" || identifiers[i] !~ /^[0-9A-Za-z-]+$/) {
        return 0
      }
      if (identifiers[i] ~ /^[0-9]+$/ && length(identifiers[i]) > 1 && substr(identifiers[i], 1, 1) == "0") {
        return 0
      }
    }
  }
  return 1
}

function is_date(value, month, day) {
  if (value !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) {
    return 0
  }
  month = substr(value, 6, 2) + 0
  day = substr(value, 9, 2) + 0
  return month >= 1 && month <= 12 && day >= 1 && day <= 31
}

function is_allowed_category(value) {
  return value == "### ⚠️ Breaking Changes / 破坏性变更" ||
    value == "### 🚀 New Features / 新功能" ||
    value == "### 🐛 Bug Fixes / 问题修复" ||
    value == "### 🔧 Improvements / 改进" ||
    value == "### ⬆️ Upgrade Notes / 升级说明" ||
    value == "### 🔒 Security / 安全" ||
    value == "### 📚 Documentation / 文档" ||
    value == "### ⚠️ Known Issues / 已知问题"
}

function finish_category() {
  if (section_kind != "" && category != "" && category_entries == 0) {
    fail("changelog category must contain at least one list entry: " category)
  }
  category = ""
  category_entries = 0
}

function finish_section() {
  finish_category()
  if (section_kind == "release" && (section_categories == 0 || section_entries == 0)) {
    fail("release section must contain at least one allowed category and list entry: " section_version)
  }
  section_kind = ""
  section_version = ""
  section_categories = 0
  section_entries = 0
}

BEGIN {
  failed = 0
  in_fence = 0
  fence = ""
  section_kind = ""
  unreleased_count = 0
  target_count = 0
  target_active = 0
  output_count = 0
  section_sequence = 0
}

{
  if (index($0, "\r") != 0) {
    fail("CRLF is not allowed; use UTF-8 with LF line endings")
  }
  if (NR == 1 && $0 != "# Changelog") {
    fail("first line must be exactly # Changelog")
  }

  if (in_fence) {
    if (target_active) {
      output[++output_count] = $0
    }
    if (substr($0, 1, 3) == fence) {
      in_fence = 0
      fence = ""
    }
    next
  }
  if (substr($0, 1, 3) == "```" || substr($0, 1, 3) == "~~~") {
    in_fence = 1
    fence = substr($0, 1, 3)
    if (target_active) {
      output[++output_count] = $0
    }
    next
  }

  if ($0 ~ /^## /) {
    target_active = 0
    finish_section()
    section_sequence++

    if ($0 == "## [Unreleased]") {
      unreleased_count++
      if (unreleased_count != 1 || section_sequence != 1) {
        fail("[Unreleased] must appear exactly once before every release section")
      }
      section_kind = "unreleased"
      next
    }

    marker = index($0, "] - ")
    if (substr($0, 1, 4) != "## [" || marker == 0) {
      fail("version heading must match ## [vMAJOR.MINOR.PATCH] - YYYY-MM-DD")
    }
    tag = substr($0, 5, marker - 5)
    date = substr($0, marker + 4)
    if (!is_semver(tag) || !is_date(date)) {
      fail("invalid SemVer release heading or date: " $0)
    }
    if (unreleased_count != 1) {
      fail("release sections must follow [Unreleased]")
    }
    if (seen_version[tag]++) {
      fail("duplicate release version: " tag)
    }

    section_kind = "release"
    section_version = tag
    if (tag == version) {
      target_count++
      target_active = 1
    }
    next
  }

  if ($0 ~ /^### /) {
    if (section_kind == "") {
      fail("category appears outside [Unreleased] or a release section")
    }
    finish_category()
    if (!is_allowed_category($0)) {
      fail("unsupported release category: " $0)
    }
    category = $0
    category_entries = 0
    section_categories++
    category_key = section_sequence SUBSEP category
    if (seen_category[category_key]++) {
      fail("duplicate category in one section: " category)
    }
  } else if ($0 ~ /^- .+/) {
    if (category == "") {
      fail("release entry must belong to an allowed category")
    }
    category_entries++
    section_entries++
  }

  if (target_active) {
    output[++output_count] = $0
  }
}

END {
  if (failed) {
    exit 1
  }
  if (in_fence) {
    fail("unterminated fenced code block")
  }
  finish_section()
  if (unreleased_count != 1) {
    fail("[Unreleased] must appear exactly once")
  }
  if (version != "") {
    if (target_count != 1) {
      fail("target version must appear exactly once: " version)
    }
    first = 1
    while (first <= output_count && output[first] ~ /^[[:space:]]*$/) {
      first++
    }
    last = output_count
    while (last >= first && output[last] ~ /^[[:space:]]*$/) {
      last--
    }
    if (first > last) {
      fail("target release body is empty: " version)
    }
    for (i = first; i <= last; i++) {
      print output[i]
    }
  }
}
