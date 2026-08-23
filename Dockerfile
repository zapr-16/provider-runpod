FROM gcr.io/distroless/static:nonroot
# The :nonroot base already defaults to uid 65532; declared explicitly so
# scanners (and humans) can verify the container never runs as root. The
# numeric form is required: kubelet cannot verify runAsNonRoot against a
# named user and fails the pod with CreateContainerConfigError.
USER 65532:65532
COPY provider /provider
ENTRYPOINT ["/provider"]
