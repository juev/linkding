"""Compare bookmark list responses from pinned Python linkding and the Go server.

Both servers must see the same database contents. Start Go from a SQLite backup
or an equivalent PostgreSQL fixture. Supply an existing API token explicitly.
"""

import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request


QUERIES = [
    {},
    {"q": "ёж"},
    {"q": "ЁЖ"},
    {"q": "istanbul"},
    {"q": "İSTANBUL"},
    {"q": "Σίσυφος"},
    {"q": "σίσυφος"},
    {"q": "% underscore"},
    {"q": "#unicode"},
    {"q": "not #unicode"},
    {"q": "!untagged"},
    {"q": "(Greek or Unicode) !unread"},
    {"q": "plain or Über"},
    {"q": "not !unknown"},
    {"q": "!unknown or Über"},
    {"q": "Über or"},
    {"sort": "title_asc"},
    {"sort": "title_desc"},
    {"sort": "modified_asc"},
    {"sort": "title_asc", "limit": "2", "offset": "2"},
]


def normalize(value, origin):
    if isinstance(value, str) and value.startswith(origin + "/"):
        return "{BASE}" + value[len(origin):]
    if isinstance(value, list):
        return [normalize(item, origin) for item in value]
    if isinstance(value, dict):
        return {key: normalize(item, origin) for key, item in value.items()}
    return value


def fetch(origin, token, params):
    query = urllib.parse.urlencode(params)
    request = urllib.request.Request(
        origin + "/api/bookmarks/?" + query,
        headers={"Authorization": "Token " + token},
    )
    try:
        response = urllib.request.urlopen(request, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        body = response.read()
        try:
            decoded = normalize(json.loads(body), origin)
        except json.JSONDecodeError:
            decoded = body.decode("utf-8", errors="replace")
        headers = {
            name: response.headers.get(name)
            for name in ("Allow", "Content-Type", "Content-Language")
        }
        return response.status, headers, decoded


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", required=True, help="Python server origin")
    parser.add_argument("--candidate", required=True, help="Go server origin")
    parser.add_argument("--token", required=True, help="API token in both databases")
    args = parser.parse_args()
    reference = args.reference.rstrip("/")
    candidate = args.candidate.rstrip("/")
    mismatches = 0
    for params in QUERIES:
        expected = fetch(reference, args.token, params)
        actual = fetch(candidate, args.token, params)
        if expected != actual:
            mismatches += 1
            print("MISMATCH", json.dumps(params, ensure_ascii=False), file=sys.stderr)
            print("  Python:", repr(expected), file=sys.stderr)
            print("  Go:    ", repr(actual), file=sys.stderr)
    print(f"Compared {len(QUERIES)} bookmark queries; mismatches: {mismatches}")
    return 1 if mismatches else 0


if __name__ == "__main__":
    raise SystemExit(main())
