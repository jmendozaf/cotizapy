# cotizapy 🇵🇾

Cotizaciones del guaraní, de la DNIT (ex SET) y del Banco Central del Paraguay, con todo el historial. Las descarga, las guarda en SQLite y las expone por línea de comandos, API HTTP o como dataset estático en JSON y CSV.

```sh
$ cotiza get usd 2026-10-11
{
  "bcp": {
    "source": "bcp",
    "currency": "USD",
    "date": "2026-10-09",
    "reference": 5694.47,
    "requested_date": "2026-10-11",
    "exact": false
  },
  "set": {
    "source": "set",
    "currency": "USD",
    "date": "2026-10-09",
    "buy": 5678.11,
    "sell": 5698.34,
    "requested_date": "2026-10-11",
    "exact": false
  }
}
```

- **Las dos fuentes oficiales**: la DNIT (compra y venta, publicada para fines tributarios) y el BCP (cotización referencial).
- **Todo el historial**: DNIT desde enero de 2010 (6 monedas) y BCP desde 2001 (22 monedas, incluidos oro y DEG). Unas 150.000 cotizaciones sincronizadas en ~30 segundos.
- **Consulta por fecha con la regla resuelta**: si pedís un domingo o un feriado, devuelve la última cotización publicada antes y te dice de qué fecha es (`date` vs `requested_date`).
- **Dataset abierto**: JSON y CSV en GitHub Pages, actualizado cuatro veces por día por GitHub Actions. Se usa con un `fetch` o desde Excel, sin instalar nada.
- **Un solo binario** sin dependencias, o una imagen Docker.

## Dataset en JSON y CSV

Sin instalar nada: **https://jmendozaf.github.io/cotizapy/**

| Archivo | Contenido |
|---|---|
| `latest.json` | Última cotización de cada fuente y moneda |
| `set/USD/latest.json` | Último dólar de la DNIT |
| `set/USD/2026.json` | Un año, indexado por fecha: `{"rates": {"2026-10-09": {"buy": 5678.11, "sell": 5698.34}}}` |
| `bcp/EUR/2026.csv` | Un año en CSV |
| `bcp/EUR.csv` | Todo el historial en CSV |
| `index.json` | Fuentes, monedas, primera y última fecha de cada una |

```js
const { rates } = await fetch("https://jmendozaf.github.io/cotizapy/set/USD/2026.json").then(r => r.json());
rates["2026-10-09"].sell; // 5698.34
```

En Google Sheets: `=IMPORTDATA("https://jmendozaf.github.io/cotizapy/set/USD.csv")`.

Los montos son números exactos tal como los publica la fuente (nunca pasan por punto flotante).

## Instalación

### Desde el código

```sh
git clone https://github.com/jmendozaf/cotizapy && cd cotizapy
docker compose up -d     # API en localhost:8080, se sincroniza cada 6 horas
```

### Binario

```sh
make binaries            # Linux, macOS y Windows en ./dist (compila en Docker)
sudo mv dist/cotiza-darwin-arm64 /usr/local/bin/cotiza
```

## Primeros pasos

**1. Descargar todo** (~30 segundos, crea una base de ~7 MB)

```sh
export COTIZA_DB=~/.local/share/cotiza.db   # agregalo a tu ~/.zshrc o ~/.bashrc para dejarlo fijo
mkdir -p ~/.local/share
cotiza sync
```

**2. Consultar**

```sh
cotiza get usd                         # hoy, las dos fuentes
cotiza get eur 2026-09-15 --source bcp # una fecha, una fuente
cotiza history brl --from 2026-01-01 --csv > brl.csv
cotiza latest                          # última cotización de cada moneda
cotiza series                          # monedas disponibles y rango de fechas
```

**3. Mantenerlo al día**

```sh
cotiza sync
```

La primera vez baja todo el historial. Después, la DNIT se lee completa (es una sola página) y del BCP solo el año en curso (y el anterior durante enero). Con `cotiza serve --sync-every 6h` (o la imagen Docker) se hace solo.

## API

```sh
cotiza serve --sync-every 6h
curl localhost:8080/v1/usd
```

| Método | Ruta | Respuesta |
|---|---|---|
| `GET` | `/v1/{moneda}?date=2026-10-11` | Cotización vigente en las dos fuentes. Sin `date`, hoy (hora de Asunción). |
| `GET` | `/v1/{set\|bcp}/{moneda}?date=2026-10-11` | Cotización vigente en una fuente. `404` si no hay ninguna en los 10 días anteriores. |
| `GET` | `/v1/{set\|bcp}/{moneda}/history?from=2026-09-01&to=2026-09-30` | Cotizaciones publicadas en el rango (por defecto, los últimos 30 días). |
| `GET` | `/v1/latest` | Última cotización de cada fuente y moneda. |
| `GET` | `/v1/series` | Monedas, rango de fechas y hora de la última sincronización. |
| `GET` | `/healthz` | `{"status":"ok"}` |

Las monedas van en código ISO (`usd`, `EUR`, `brl`…). Errores siempre como `{"error": "...", "message": "..."}`. Respuestas con CORS abierto.

## Las fuentes

| | DNIT (ex SET) | BCP |
|---|---|---|
| Página | [Cotizaciones](https://www.dnit.gov.py/web/portal-institucional/cotizaciones) | [Cotización Referencial Histórica](https://www.bcp.gov.py/webapps/web/cotizacion/monedas-historica) |
| Qué publica | Compra y venta | Referencial (promedio ponderado del mercado interbancario) |
| Días | Todos: fines de semana y feriados repiten el último día hábil | Solo días hábiles |
| Monedas | USD, BRL, ARS, JPY, EUR, GBP | 22, incluidas oro (XAU) y DEG (SDR) |
| Desde | 2010 | 2001 |
| Formato | Una página con una tabla por mes | Una página por año y moneda |

### Lo que corrige el lector

Las tablas se cargan a mano y no son uniformes. cotizapy lo resuelve así:

- **Formato numérico por fila**: algunos meses de la DNIT están en formato inglés (`7,299.26`) y la mayoría en castellano (`7.299,26`). Como `6.507` significa 6507 en una fila y 6,507 en la otra, el formato se decide mirando la fila entera.
- **Días repetidos**: abril de 2021 lista los días 24 a 26 dos veces, y el primer 26 repite el viernes. Se usa la fila posterior y se avisa.
- **Días sin cero** (`1` en lugar de `01`), filas vacías y `ND` del BCP.

Los valores no se modifican: si la fuente publicó un error de tipeo (por ejemplo, el peso chileno del BCP del 23/03/2012, 100 veces más alto), queda tal cual.

## Desarrollo

No hace falta tener Go instalado: todo corre en Docker.

```sh
make test       # tests (con páginas reales de la DNIT y el BCP en testdata/)
make image      # imagen Docker
make binaries   # binarios para Linux, macOS y Windows en ./dist
```

Con Go 1.27 local: `go test ./...` y `go build ./cmd/cotiza`.

El dataset lo publica [`.github/workflows/dataset.yml`](.github/workflows/dataset.yml) en la rama `gh-pages`. La base viaja entre corridas en el cache de Actions; si se pierde, la siguiente corrida baja todo de nuevo.

## Licencia

MIT © Javier Mendoza. Los datos son publicados por la Dirección Nacional de Ingresos Tributarios (DNIT) y el Banco Central del Paraguay (BCP).
