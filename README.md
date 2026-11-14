# openhexa-essence-back

API du domaine carburants de l'écosystème OpenHexa, en Go (réécriture de l'API FastAPI).

Interroge l'API JSON `data.economie.gouv.fr` (dataset "Prix des carburants en
France - Flux instantané - v2"), extrait les stations-service et leurs prix, et
les indexe dans Elasticsearch, via deux mécanismes complémentaires :

- **Synchronisation de fond** : boucle de polling démarrée avec l'application,
  toutes les `POLLING_INTERVAL_SECONDS` (quotidienne par défaut) — filet de
  sécurité garantissant une resynchronisation même sans trafic. Au démarrage, la
  première ingestion est sautée si l'index contient déjà des données (cold start
  en scale-to-zero).
- **Rafraîchissement à la demande** : chaque recherche (`GET /api/v1/stations/search`)
  déclenche, en tâche de fond, une réingestion si la dernière remonte à plus de
  `SEARCH_REFRESH_TTL_SECONDS` (10 min par défaut). Cette cadence correspond à la
  fréquence de republication officielle du flux gouvernemental (*"Le flux de
  données instantané est mis à jour toutes les 10 minutes"*) — inutile de
  redétélécharger plus souvent. Le rafraîchissement ne bloque jamais la
  recherche qui l'a déclenché : celle-ci répond avec les données actuelles, la
  recherche suivante bénéficiera des données fraîches.

## Contrat d'API

Identique à la version Python : mêmes routes, mêmes paramètres, mêmes champs JSON
(`null` explicites inclus), mêmes `_id` Elasticsearch — la réécriture est
transparente pour `essence-front` et pour les documents déjà indexés.

| Route | Description |
|---|---|
| `GET /api/v1/stations/search` | `carburant`, `lat`, `lon`, `radius_km` (10), `tri` (`score`\|`prix`\|`distance`\|`recent`), `prix_max`, `size` (20), `search_after` (répétable) |
| `GET /api/v1/stations/{station_id}` | fiche d'une station, 404 `{"detail": "Station introuvable"}` sinon |
| `GET /api/v1/status` | `{"stations": true}` quand au moins une station est indexée |

Erreurs au format FastAPI (`{"detail": ...}`), CORS permissif en lecture seule.

### Différences avec la version Python

- `size` hors de `[1, 1000]` répond **422** (la version Python répondait 500, via une
  exception non gérée de `core`).
- Les 422 reprennent la forme `detail: [{loc, msg, type}]` de FastAPI, mais les
  messages sont simplifiés.
- Pas de `/docs` ni de `/openapi.json` auto-générés.
- `radius_km` est envoyé à Elasticsearch sous la forme `5km` et non `5.0km`
  (équivalent pour Elasticsearch).
- Une station dont la date de mise à jour n'est pas une chaîne est ignorée (la
  version Python l'ignorait aussi, via une `TypeError`).
- Le serveur s'arrête proprement sur `SIGTERM` (les requêtes en cours terminent,
  le polling et les rafraîchissements sont interrompus).

## Configuration

Variables d'environnement (voir `.env.example`, chargé automatiquement s'il existe) :

| Variable | Défaut |
|---|---|
| `ES_URL` | — (obligatoire) |
| `ES_API_KEY` ou `ES_USER` + `ES_PASSWORD` | — (l'API key est préférée) |
| `ES_INDEX_PREFIX` | `openhexa` |
| `DATA_GOUV_LIVE_URL` | export JSON du flux instantané v2 |
| `POLLING_INTERVAL_SECONDS` | `86400` |
| `SEARCH_REFRESH_TTL_SECONDS` | `600` |
| `PORT` | `8001` |

## Développement

La dépendance à [`core`](https://github.com/OpenHexa-fr/core) passe par un `replace`
vers `../core` dans `go.mod` tant que `core` n'est pas publié comme module versionné :
cloner les deux repos côte à côte.

```bash
cp .env.example .env
go run ./cmd/essence-back
```

## Tests / qualité

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

Les tests n'ont besoin d'aucun Elasticsearch : `core/estest` simule le cluster.

## Docker

```bash
# Dev local : depuis la racine du monorepo (core/ et essence-back/ côte à côte)
docker build -f essence-back/Dockerfile -t openhexa-essence-back .

# Contexte = ce repo seul, core est cloné à la référence CORE_REF
docker build -f Dockerfile.deploy --build-arg CORE_REF=main -t openhexa-essence-back .
```

L'image finale est `distroless/static` (binaire statique, sans shell).

## Publier `core` comme module versionné

Une fois `core` tagué (`v0.1.0`) :

```bash
go mod edit -dropreplace github.com/openhexa-fr/core
go get github.com/openhexa-fr/core@v0.1.0
```

puis supprimer le `git clone` de `Dockerfile.deploy`, le checkout de `core` et
`CORE_REF` dans `.github/workflows/test.yml`.
