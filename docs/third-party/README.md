# Third-party files

`internal/auth/common-passwords.txt.gz` is an unchanged copy of Django 6.0.7's `django/contrib/auth/common-passwords.txt.gz` (SHA-256: `3c1baed62596de36860824eb3f436d5932d37ca8b06e59df78f5a44ec175afe4`). It lets the Go server apply the same common-password check as linkding v1.47.0.

`web/static/admin/` contains Django Admin 6.0.7 static files copied from the linkding v1.47.0 environment for the admin UI. Django is licensed under BSD-3-Clause; its license is preserved in [django-LICENSE](django-LICENSE). Files under `web/static/admin/vendor/` carry their own license notices.

`internal/httpserver/admin_locale/` contains compiled `django.mo` catalogs from Django 6.0.7 (`django/conf/locale` and the admin, auth, contenttypes, and sessions apps) and Django REST framework 3.17.2. They came from the pinned linkding v1.47.0 environment and supply Go Admin translations. Both projects use BSD-3-Clause; see [django-LICENSE](django-LICENSE) and [djangorestframework-LICENSE.md](djangorestframework-LICENSE.md).

The plus Docker image installs SingleFile CLI 2.0.75 and downloads uBlock Origin Lite 2026.920.1710 (Chromium ZIP SHA-256: `3ebf1458078d8738daf580e5ddeb41412cfa20fe4874a2fb321373f5ff7a09f1`) during the build. Their license files remain in the image at `/usr/local/lib/node_modules/single-file-cli/LICENSE` (AGPL-3.0) and `/etc/linkding/uBOLite.chromium.mv3/LICENSE.txt` (GPL-3.0). The installed Chromium package retains its Debian copyright notices.

The plus image removes SingleFile CLI's `args.push("--single-process");` line from `/usr/local/lib/node_modules/single-file-cli/lib/browser.js` during the Docker build. That flag caused navigation timeouts in a local container reproduction; the patched image saved the same page, including after a restart. The installed package retains its JavaScript source and AGPL-3.0 license, and both Dockerfiles contain the exact build-time change.
