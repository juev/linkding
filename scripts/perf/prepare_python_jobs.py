"""Prepare a disposable Python fixture with queued metadata refresh tasks."""

import os

from bookmarks.models import Bookmark
from bookmarks.services.tasks import _refresh_metadata_task


origin = os.environ["BENCH_MOCK_ORIGIN"].rstrip("/")
count = int(os.environ.get("BENCH_JOB_COUNT", "100"))
for bookmark_id in range(1, count + 1):
    url = f"{origin}/python/page/{bookmark_id}"
    updated = Bookmark.objects.filter(id=bookmark_id).update(
        url=url, url_normalized=url, title=""
    )
    if updated != 1:
        raise RuntimeError(f"bookmark {bookmark_id} missing")
    _refresh_metadata_task(bookmark_id)
print(f"queued metadata jobs={count}")
