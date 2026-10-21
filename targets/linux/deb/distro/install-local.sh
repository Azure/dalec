# The proxy helpers are prepended by Go.

sort_rank_versions() {
	local version i
	local -a sorted=()

	for version in "$@"; do
		for ((i = 0; i < ${#sorted[@]}; i++)); do
			if dpkg --compare-versions "$version" eq "${sorted[i]}"; then
				continue 2
			fi

			if dpkg --compare-versions "$version" gt "${sorted[i]}"; then
				sorted=("${sorted[@]:0:i}" "$version" "${sorted[@]:i}")
				continue 2
			fi
		done

		sorted+=("$version")
	done

	if (( ${#sorted[@]} )); then
		printf '%s\n' "${sorted[@]}"
	fi
}

set -euo pipefail
export LC_ALL=C

needs_cleanup=0

cleanup() {
	local rc=$?

	cleanup_apt_proxy

	if [[ "$needs_cleanup" = 1 ]]; then
		apt remove -y aptitude || {
			local cleanup_rc=$?

			if [[ "$rc" = 0 ]]; then
				rc="$cleanup_rc"
			fi
		}
	fi

	exit "$rc"
}

trap cleanup EXIT
configure_apt_proxy

rm -f /var/lib/apt/lists/_*
apt autoclean -y
rm -rf /var/lib/apt/lists/partial/*
apt update

if [[ "${DALEC_UPGRADE}" = true ]]; then
	apt dist-upgrade -y
fi

# Expand the local directory here, not in the exec argv.
declare -a debs=("$1"/*.deb)
if apt install -y "${debs[@]}"; then
	exit 0
fi

if ! command -v aptitude >/dev/null; then
	needs_cleanup=1
	apt install -y aptitude
fi

dpkg -i --force-depends "${debs[@]}"

# Native query branches for the original local packages, not repository versions.
declare -a anchors=()

# Resolver options protecting anchors and preferring newer direct unmet targets.
declare -a resolver_opts=()

# Identify each local package by name and architecture for the unmet-dependency
# query. Reject removing it or changing its version so aptitude must satisfy
# the dependencies declared by the original local package.
for file in "${debs[@]}"; do
	name="$(dpkg-deb -f "$file" Package)"
	version="$(dpkg-deb -f "$file" Version)"
	arch="$(dpkg-deb -f "$file" Architecture)"

	anchor="?and(?exact-name($name),?architecture($arch))"
	anchors+=("$anchor")

	resolver_opts+=(-o "Aptitude::ProblemResolver::Hints::=reject $anchor :UNINST")

	# Replacing the local version could discard its original constraints.
	# Reject both older and newer versions, leaving only the original version
	# eligible for this package and architecture.
	# Use two comparisons: aptitude 0.8.13 treats <> as equality.
	resolver_opts+=(-o "Aptitude::ProblemResolver::Hints::=reject $anchor <$version")
	resolver_opts+=(-o "Aptitude::ProblemResolver::Hints::=reject $anchor >$version")
done

# Find versions matching unmet direct dependencies of the installed local
# packages. Let aptitude interpret alternatives, providers and architectures;
# do not include unrelated broken packages or recurse into transitive dependencies.
query="?broken-reverse-depends(?and(?installed,?or($(IFS=,; echo "${anchors[*]}"))))"
rows="$(aptitude versions --disable-columns --group-by=none --show-package-names=always -F '%p %E' "$query")" || {
	rc=$?

	# No matching versions is not a failed repair: transitive dependencies may
	# still be broken. Other query/cache failures must retain their exit status.
	[[ "$rc" = 2 ]] || exit "$rc"

	rows=
}

# Native rows contain concrete name/version/architecture triples. %E distinguishes
# Architecture: all from native versions, both of which have unqualified names.
declare -A target_versions=()

while read -r name version arch; do
	[[ -n "$version" ]] || continue

	target="?and(?exact-name(${name%%:*}),?architecture($arch))"
	target_versions["$target"]+="$version "
done <<< "$rows"

targets="$(printf '%s\n' "${!target_versions[@]}" | sort)"

# Versions of one concrete target, sorted before generating its score hints.
declare -a versions=()

# Sort and deduplicate each target's versions using Debian comparison rules.
# Apply small score penalties: -1 for the newest, -2 for the next, and so on.
# Native solution cost remains primary; these preferences affect secondary
# scoring alongside native preferences, not a strict no-removal guarantee.
while IFS= read -r name; do
	[[ -n "$name" ]] || continue

	read -r -a versions <<< "${target_versions[$name]}"
	sorted="$(sort_rank_versions "${versions[@]}")"
	[[ -n "$sorted" ]] || continue

	mapfile -t versions <<< "$sorted"
	for ((rank = 1; rank <= ${#versions[@]}; rank++)); do
		resolver_opts+=(-o "Aptitude::ProblemResolver::Hints::=-$rank $name ${versions[rank-1]}")
	done
done <<< "$targets"

# Score hints are not extra dependency constraints or mandatory requests for
# every alternative/provider. Leave the native/default or custom cost policy
# intact while aptitude solves the whole graph.
aptitude install -y -f "${resolver_opts[@]}"
