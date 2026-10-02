#!/usr/bin/env bash
# Rejects workflow files that GitHub itself will refuse.
#
# Why this exists: `permissions:` inside a step is not valid. GitHub rejects the
# whole file, creates no job, and reports a failed run on *every* push to any
# branch with "This run likely failed because of a workflow file issue". Every
# other check in this repository stayed green while the release pipeline had not
# run once since that line was added. This script is the guard for it.
#
# It checks what actually breaks in practice and says so, rather than pretending
# to be a full schema validator:
#   1. the file parses as YAML
#   2. no duplicate keys (PyYAML silently keeps the last one, GitHub errors)
#   3. no `permissions` inside a step (workflow and job level are valid)
#
# Full validation would need the published JSON schema vendored into the
# repository, which is a third-party artifact to keep current. If that is ever
# worth the cost, this is where to swap it in.
set -euo pipefail

cd "$(dirname "$0")/.."

# Reads one workflow path on argv[1]. Exit 0 clean, 1 a real problem, 2 cannot run.
check_one() {
	python3 - "$1" <<'PY'
import sys

try:
    import yaml
except ImportError:
    print("PyYAML non disponibile")
    sys.exit(2)

path = sys.argv[1]


class Strict(yaml.SafeLoader):
    pass


def no_dup(loader, node, deep=False):
    mapping = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in mapping:
            raise yaml.constructor.ConstructorError(
                "mentre costruisco una mappa", node.start_mark,
                "chiave duplicata %r" % (key,), key_node.start_mark)
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


Strict.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, no_dup)

try:
    with open(path) as fh:
        doc = yaml.load(fh, Loader=Strict)
except Exception as exc:                                    # noqa: BLE001
    print("YAML non valido: %s" % exc)
    sys.exit(1)

if not isinstance(doc, dict):
    print("il file non e' una mappa di workflow")
    sys.exit(1)

jobs = doc.get("jobs")
if not isinstance(jobs, dict) or not jobs:
    print("manca un blocco 'jobs' vuoto")
    sys.exit(1)

problems = []
for job_id, job in jobs.items():
    if not isinstance(job, dict):
        continue
    for step in job.get("steps") or []:
        if not isinstance(step, dict):
            continue
        if "permissions" in step:
            problems.append(
                "job '%s', step '%s': 'permissions' non e' ammesso a livello di "
                "step. GitHub rifiuta l'intero file e non esegue nessun job. "
                "Sposta il permesso sul job." % (job_id, step.get("name", "?")))

for problem in problems:
    print(problem)
sys.exit(1 if problems else 0)
PY
}

status=0
checked=0

shopt -s nullglob
for wf in .github/workflows/*.yml .github/workflows/*.yaml; do
	checked=$((checked + 1))
	if out=$(check_one "$wf"); then
		continue
	else
		rc=$?
		if [ "$rc" -eq 2 ]; then
			printf '  %s: %s (controllo saltato)\n' "$wf" "$out"
			continue
		fi
		printf '❌ %s\n' "$wf"
		printf '%s\n' "$out" | sed 's/^/   /'
		status=1
	fi
done
shopt -u nullglob

if [ "$checked" -eq 0 ]; then
	echo "check_workflows: nessun workflow trovato in .github/workflows/" >&2
	exit 1
fi

if [ "$status" -ne 0 ]; then
	echo "check_workflows: uno o piu' workflow sarebbero rifiutati da GitHub" >&2
	exit 1
fi

echo "check_workflows: $checked workflow validi"