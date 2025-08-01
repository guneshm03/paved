# Product catalog service

Serves product details, prices and stock levels to the storefront. Data is
loaded nightly from the merchandising system into the catalog database and
cached aggressively at the edge.

## Endpoints

- GET /products/{sku}
- GET /products?category=...
- GET /availability/{sku}

## Running

Create a virtualenv, install requirements.txt, and run the app module with the
development settings. The service needs read access to the catalog database.
There is no CI pipeline yet; tests are run by hand before deploys.
