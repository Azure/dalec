#!/usr/bin/env bash

set -eux

do_install() {
	local parent="${1}"
	shift

	local dest="${1}"
	shift

	mkdir -p "${parent}"

	local files=($@)

	# When the number of files passed in is more than 1, then dest *must* refer
	# to a directory
	if test ${#files[@]} -gt 1; then
		mkdir -p "${dest}"
	fi

	for src in ${files[@]}; do
		cp --reflink=auto -a "${src}" "${dest}"
	done
}
