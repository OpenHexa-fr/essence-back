# openhexa-essence-back

API FastAPI du domaine carburants de l'écosystème OpenHexa.

Interroge en polling `https://donnees.roulez-eco.fr/opendata/instantane` (archive
ZIP contenant un fichier XML), extrait les stations-service et leurs prix, et les
indexe dans Elasticsearch.

## Installation (développement)

```bash
pip install -e ../core
pip install -e ".[dev]"
cp .env.example .env
```

## Lancer l'API

```bash
uvicorn app.main:app --reload --port 8001
```

Le polling démarre automatiquement au lifespan de l'application, toutes les
`POLLING_INTERVAL_SECONDS` secondes.

## Tests / qualité

```bash
pytest
ruff check .
mypy .
```
