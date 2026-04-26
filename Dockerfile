FROM gcr.io/distroless/static:nonroot
COPY provider /provider
ENTRYPOINT ["/provider"]
