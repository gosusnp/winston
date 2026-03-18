# Copyright 2026 Jimmy Ma
# SPDX-License-Identifier: MIT

FROM gcr.io/distroless/static:nonroot
COPY winston /winston
USER nonroot:nonroot
ENTRYPOINT ["/winston"]
