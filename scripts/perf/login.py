"""Write a disposable benchmark session cookie using the normal login form."""

import argparse
from http.cookiejar import CookieJar
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import HTTPCookieProcessor, Request, build_opener


parser = argparse.ArgumentParser()
parser.add_argument("base_url")
parser.add_argument("output")
parser.add_argument("--username", default="bench")
parser.add_argument("--password", default="benchmark-only-password")
args = parser.parse_args()

origin = args.base_url.rstrip("/")
jar = CookieJar()
opener = build_opener(HTTPCookieProcessor(jar))
with opener.open(origin + "/login/", timeout=20) as response:
    if response.status != 200:
        raise RuntimeError(f"login page returned {response.status}")
csrf = next((cookie.value for cookie in jar if cookie.name == "ld_csrftoken"), None)
if not csrf:
    raise RuntimeError("login page did not set a CSRF cookie")
form = urlencode(
    {"username": args.username, "password": args.password, "csrfmiddlewaretoken": csrf}
).encode("ascii")
request = Request(
    origin + "/login/",
    data=form,
    headers={
        "Content-Type": "application/x-www-form-urlencoded",
        "Origin": origin,
        "Referer": origin + "/login/",
    },
)
with opener.open(request, timeout=20) as response:
    if response.status != 200:
        raise RuntimeError(f"login returned {response.status}")
session = next((cookie.value for cookie in jar if cookie.name == "ld_sessionid"), None)
if not session:
    raise RuntimeError("login did not set a session cookie")
Path(args.output).write_text(f"ld_sessionid={session}\n", encoding="ascii")
print("benchmark session created")
