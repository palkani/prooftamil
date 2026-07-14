# Python ML service (cascade Tier 1). Build context = repo root.
FROM python:3.11-slim AS build

WORKDIR /src

# Install into a self-contained venv we can copy wholesale into the runtime
# image, leaving the build toolchain behind.
ENV VIRTUAL_ENV=/opt/venv
RUN python -m venv $VIRTUAL_ENV
ENV PATH="$VIRTUAL_ENV/bin:$PATH"

COPY apps/ml/pyproject.toml ./
COPY apps/ml/app ./app
RUN pip install --no-cache-dir --upgrade pip && pip install --no-cache-dir .

FROM python:3.11-slim

# Non-root: Cloud Run does not require it, but a container escape shouldn't
# start as root.
RUN useradd --create-home --uid 10001 appuser

ENV VIRTUAL_ENV=/opt/venv
ENV PATH="$VIRTUAL_ENV/bin:$PATH"
ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1

COPY --from=build /opt/venv /opt/venv
COPY --from=build /src/app /app/app

WORKDIR /app
USER appuser

EXPOSE 8081

# Cloud Run sets PORT; honour it, defaulting to 8081 for local runs.
CMD ["sh", "-c", "uvicorn app.main:app --host 0.0.0.0 --port ${PORT:-8081} --workers ${WORKERS:-2}"]
