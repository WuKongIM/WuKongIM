#!/bin/bash

# Test script to verify changelog extraction logic
# This simulates what the GitHub Actions workflow will do

VERSION="${1:-v2.2.1-20250624}"
CHANGELOG_FILE="CHANGELOG.md"

echo "Testing changelog extraction for version: $VERSION"
echo "================================================"

# Find the line number where this version starts
START_LINE=$(grep -n "^## \[$VERSION\]" "$CHANGELOG_FILE" | cut -d: -f1)

if [ -z "$START_LINE" ]; then
  echo "ERROR: Version $VERSION not found in CHANGELOG.md"
  exit 1
fi

echo "Found version at line: $START_LINE"

# Find the line number where the next version starts (or end of file)
NEXT_VERSION_LINE=$(tail -n +$((START_LINE + 1)) "$CHANGELOG_FILE" | grep -n "^## \[" | head -1 | cut -d: -f1)

if [ -z "$NEXT_VERSION_LINE" ]; then
  echo "No next version found, extracting to end of file"
  # No next version found, extract to end of file
  sed -n "${START_LINE},\$p" "$CHANGELOG_FILE" > release_notes_test.md
else
  # Calculate the actual line number
  END_LINE=$((START_LINE + NEXT_VERSION_LINE - 1))
  echo "Next version found at relative line: $NEXT_VERSION_LINE (absolute: $END_LINE)"
  # Extract the section, excluding the next version header
  sed -n "${START_LINE},$((END_LINE - 1))p" "$CHANGELOG_FILE" > release_notes_test.md
fi

# Remove the version header line
sed -i.bak '1d' release_notes_test.md

# Remove trailing --- separators
sed -i.bak '/^---$/d' release_notes_test.md

# Trim trailing empty lines using a simpler approach
# Use perl which is more portable and reliable
perl -i -pe 'chomp if eof' release_notes_test.md

# Alternative: use awk to remove trailing blank lines
awk 'NF {p=1} p' release_notes_test.md > release_notes_test.tmp && mv release_notes_test.tmp release_notes_test.md

# Clean up backup files
rm -f release_notes_test.md.bak

echo ""
echo "Extraction complete! Preview of release_notes_test.md:"
echo "================================================"
head -50 release_notes_test.md
echo ""
echo "================================================"
echo "Total lines extracted: $(wc -l < release_notes_test.md)"
echo ""
echo "Full content saved to: release_notes_test.md"

