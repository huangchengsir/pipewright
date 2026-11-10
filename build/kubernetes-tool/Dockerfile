# Build context must contain only the verified Linux binaries below. The caller
# supplies an immutable, approved Linux base with /bin/sh and the core utilities.
# No mutable fallback, network download, or inherited project credentials.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
USER 0:0
COPY --chmod=0555 helm /usr/bin/helm
COPY --chmod=0555 kubectl /usr/bin/kubectl
COPY --chmod=0555 pipewright-kube-tool /usr/bin/pipewright-kube-tool
ARG HELPER_SHA256
ARG KUBECTL_SHA256
ARG HELM_SHA256=b66cb19f2b31f9d427632ea6c4b4a1ff89b74ecaeaee323cd166da41a7af53bd
RUN set -eu; test -n "$HELPER_SHA256"; test -n "$KUBECTL_SHA256"; printf '%s  /usr/bin/pipewright-kube-tool\n%s  /usr/bin/kubectl\n%s  /usr/bin/helm\n' "$HELPER_SHA256" "$KUBECTL_SHA256" "$HELM_SHA256" | sha256sum -c -
USER 1000:1000
ENTRYPOINT ["/usr/bin/pipewright-kube-tool"]
CMD ["version"]
