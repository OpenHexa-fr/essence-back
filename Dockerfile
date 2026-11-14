# Image de développement local : le contexte de build est la racine du monorepo
# (core/ et essence-back/ côte à côte), car core n'est pas encore publié comme
# module Go versionné (voir le `replace` de go.mod).
#   docker build -f essence-back/Dockerfile -t openhexa-essence-back .
FROM golang:1.24 AS build
WORKDIR /src
COPY core/ core/
COPY essence-back/ essence-back/
WORKDIR /src/essence-back
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/essence-back ./cmd/essence-back

# distroless/static embarque les certificats CA (HTTPS vers data.economie.gouv.fr)
# et rien d'autre : pas de shell, pas de gestionnaire de paquets.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/essence-back /essence-back
EXPOSE 8001
ENTRYPOINT ["/essence-back"]
