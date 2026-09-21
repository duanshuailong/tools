#!/bin/bash

set -e
set -x


PACK_FILE_PATH=$(cd "$(dirname "$0")"; pwd)
ISO_FILE_NAME=$(ls "$PACK_FILE_PATH" | grep "^ubuntu.*iso$")
ISO_PATH=$PACK_FILE_PATH/ubuntu-files
OUTPUT_ISO=$PACK_FILE_PATH/new-ubuntu.iso

# ── Optional driver packages ────────────────────────────────────────────────
# Set to "yes" to include NVIDIA / OFED drivers in the ISO, "no" to skip.
#INCLUDE_NVIDIA="${INCLUDE_NVIDIA:-no}"
INCLUDE_NVIDIA="${INCLUDE_NVIDIA:-yes}"
#INCLUDE_OFED="${INCLUDE_OFED:-no}"
INCLUDE_OFED="${INCLUDE_OFED:-yes}"

# NVIDIA driver and OFED files – located in the drivers/ subdirectory
CUDA_FILE=$(ls "$PACK_FILE_PATH/drivers" 2>/dev/null | grep "^cuda.*run$") || true
OFED_FILE=$(ls "$PACK_FILE_PATH/drivers" 2>/dev/null | grep "^MLNX.*tgz$") || true

# ── Extract original ISO filesystem ──────────────────────────────────────
# Rename any leftover directory (which may contain root-owned files) to avoid
# conflicts, then extract to a clean location.
if [ -d "$ISO_PATH" ]; then
    mv "$ISO_PATH" "$ISO_PATH.old.$$"
fi
xorriso -osirrox on -indev "$ISO_FILE_NAME" -extract / "$ISO_PATH"

# xorriso preserves the ISO9660 read-only dir permissions (root dirs are 0555),
# so a non-root user cannot write custom content into the extracted tree. Make
# the tree writable by the owner. (When run as root this is a harmless no-op.)
chmod -R u+w "$ISO_PATH"


# ── Copy and prepare custom content ───────────────────────────────────────
cp -r "$PACK_FILE_PATH/debs" "$ISO_PATH/"
#cp -r "$PACK_FILE_PATH/scripts" "$ISO_PATH/"

# Copy NVIDIA + OFED drivers only when enabled
if [ "$INCLUDE_NVIDIA" = "yes" ] || [ "$INCLUDE_OFED" = "yes" ]; then
    cp -r "$PACK_FILE_PATH/drivers" "$ISO_PATH/"
fi

cd "$ISO_PATH"
# Checksum every deb under debs/ into md5sum.txt. Use find (not a glob) so a
# build with no debs (drivers-off, no tool groups) doesn't fail on an unexpanded
# ./debs/*/* pattern under `set -e`.
find ./debs -type f -name '*.deb' -exec md5sum {} + >> md5sum.txt || true

# Split CUDA installer if present and NVIDIA driver is enabled
if [ "$INCLUDE_NVIDIA" = "yes" ] && [ -n "$CUDA_FILE" ] && [ -f "$ISO_PATH/drivers/$CUDA_FILE" ]; then
    cd "$ISO_PATH/drivers"
    split "$CUDA_FILE" -b 3072000000 "$CUDA_FILE"
    rm "${CUDA_FILE}"
    mv "${CUDA_FILE}aa" "${CUDA_FILE}"
    mv "${CUDA_FILE}ab" "${CUDA_FILE}_a"
    cd "$ISO_PATH"
    md5sum ./drivers/* >> md5sum.txt
fi

# ── Repack ISO with full boot support ─────────────────────────────────────
# Strategy: open the original ISO as the boot template, then graft in the
# modified filesystem and write the result to a new ISO.
cd "$PACK_FILE_PATH"

# xorriso refuses to overwrite a non-empty output file; start fresh.
rm -f "$OUTPUT_ISO"

xorriso \
    -indev "$ISO_FILE_NAME" \
    -outdev "$OUTPUT_ISO" \
    \
    -boot_image any replay \
    \
    -overwrite on \
    -pathspecs on \
    -map "$ISO_PATH/" / \
    \
    -commit


# Cleanup – remove files we own, then try to remove the directory.
# If root-owned files remain, rename it so the next run can start fresh.
if [ -d "$ISO_PATH" ]; then
    # Delete everything we can (files we own, plus empty dirs on the way)
    find "$ISO_PATH" -mindepth 1 -delete 2>/dev/null || true
    # If that didn't fully clean it (root-owned subdirs remain), rename it.
    rmdir "$ISO_PATH" 2>/dev/null || mv "$ISO_PATH" "$ISO_PATH.old.$$" || true
fi
# Clean up any stale .old directories from previous runs (ignore errors).
rm -rf "$ISO_PATH.old."* 2>/dev/null || true
echo "Done: $OUTPUT_ISO"
