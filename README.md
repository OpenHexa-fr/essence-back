# openhexa-essence-back

API FastAPI du domaine carburants de l'écosystème OpenHexa.

Interroge `https://donnees.roulez-eco.fr/opendata/instantane_ruptures` (archive
ZIP contenant un fichier XML), extrait les stations-service et leurs prix, et les
indexe dans Elasticsearch, via deux mécanismes complémentaires :

- **Synchronisation de fond** : boucle de polling démarrée au lifespan de
  l'application, toutes les `POLLING_INTERVAL_SECONDS` (quotidienne par défaut) —
  filet de sécurité garantissant une resynchronisation même sans trafic.
- **Rafraîchissement à la demande** : chaque recherche (`GET /api/v1/stations/search`)
  déclenche, en tâche de fond, une réingestion si la dernière remonte à plus de
  `SEARCH_REFRESH_TTL_SECONDS` (10 min par défaut). Cette cadence correspond à la
  fréquence de republication officielle du flux gouvernemental (voir le jeu de
  données data.gouv.fr "Prix des carburants en France - Flux instantané - v2" :
  *"Le flux de données instantané est mis à jour toutes les 10 minutes"*) —
  inutile de redétélécharger plus souvent. Le rafraîchissement ne bloque jamais la
  recherche qui l'a déclenchée : celle-ci répond avec les données actuelles, la
  recherche suivante bénéficiera des données fraîches.

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
