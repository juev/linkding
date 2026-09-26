"""Seed a disposable Python linkding v1.47.0 installation via manage.py shell."""

from datetime import datetime, timezone
from pathlib import Path

from django.contrib.auth.models import User
from bookmarks.models import ApiToken, Bookmark, Tag


COUNT = 10_000
TAG_COUNT = 100
user, created = User.objects.get_or_create(username="bench")
if created:
    user.set_password("benchmark-only-password")
    user.save(update_fields=["password"])

if Bookmark.objects.filter(owner=user).exists():
    raise RuntimeError("benchmark user already has bookmarks")

now = datetime.now(timezone.utc)
Tag.objects.bulk_create(
    [Tag(owner=user, name=f"tag-{i:03d}", date_added=now) for i in range(TAG_COUNT)],
    batch_size=500,
)
tags = list(Tag.objects.filter(owner=user).order_by("name"))
for start in range(0, COUNT, 500):
    bookmarks = []
    for i in range(start, min(start + 500, COUNT)):
        url = f"https://example.org/articles/{i:05d}"
        topic = "systems" if i % 4 == 0 else "reading"
        bookmarks.append(
            Bookmark(
                owner=user,
                url=url,
                url_normalized=url,
                title=f"{topic} article {i:05d}",
                description=f"A sample {topic} description for benchmark {i:05d}",
                notes="A short personal note" if i % 10 == 0 else "",
                unread=i % 2 == 0,
                is_archived=i % 10 == 0,
                shared=i % 10 == 1,
                date_added=now,
                date_modified=now,
            )
        )
    Bookmark.objects.bulk_create(bookmarks, batch_size=500)

through = Bookmark.tags.through
relations = []
for i, bookmark_id in enumerate(
    Bookmark.objects.filter(owner=user).order_by("id").values_list("id", flat=True)
):
    relations.append(through(bookmark_id=bookmark_id, tag_id=tags[i % TAG_COUNT].id))
    relations.append(through(bookmark_id=bookmark_id, tag_id=tags[(i + 17) % TAG_COUNT].id))
through.objects.bulk_create(relations, batch_size=1000)

token, _ = ApiToken.objects.get_or_create(user=user, name="Benchmark")
Path("data/benchmark-token").write_text(token.key + "\n", encoding="ascii")
print(f"seeded bookmarks={COUNT} tags={TAG_COUNT} relations={len(relations)}")
