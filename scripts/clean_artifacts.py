# -*- coding: utf-8 -*-
"""Cross-platform artifact cleanup (make clean). Replaces `rm -rf` recipe."""
import os, shutil, sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIRS = ["bin", "data", "logs", ".apifox", ".trae-html-share-packages", "web/dist"]
FILES = ["coverage.out", "coverage_integration.out", "coverage.html", "coverage",
         "it_nocoverpkg", "jwt_coverpkg", "integration-test.log"]
FILE_GLOBS = ["wp_cov*", "*.log"]

removed = 0
for d in DIRS:
    p = os.path.join(REPO, d)
    if os.path.isdir(p):
        shutil.rmtree(p, ignore_errors=True)
        removed += 1
for f in FILES:
    p = os.path.join(REPO, f)
    if os.path.isfile(p):
        os.remove(p)
        removed += 1
import glob as g
for pat in FILE_GLOBS:
    for p in g.glob(os.path.join(REPO, pat)):
        if os.path.isfile(p):
            os.remove(p)
            removed += 1
print("cleaned %d items" % removed)
