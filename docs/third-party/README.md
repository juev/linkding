# Third-party files

`internal/auth/common-passwords.txt.gz` is an unchanged copy of Django 6.0.7's `django/contrib/auth/common-passwords.txt.gz` (SHA-256: `3c1baed62596de36860824eb3f436d5932d37ca8b06e59df78f5a44ec175afe4`). It lets the Go server apply the same common-password check as linkding v1.47.0.

`web/static/admin/` contains Django Admin 6.0.7 static files copied from the linkding v1.47.0 environment for the admin UI. Django is licensed under BSD-3-Clause; its license is preserved in [django-LICENSE](django-LICENSE). Files under `web/static/admin/vendor/` carry their own license notices.

The plus Docker image installs SingleFile CLI 2.0.75 and downloads uBlock Origin Lite during the build. Their license files remain in the image at `/usr/local/lib/node_modules/single-file-cli/LICENSE` (AGPL-3.0) and `/etc/linkding/uBOLite.chromium.mv3/LICENSE.txt` (GPL-3.0). The installed Chromium package retains its Debian copyright notices.
