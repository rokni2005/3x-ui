FROM python:3.12-slim
ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt \
 && useradd -m -u 10001 bot && mkdir -p /app/data && chown -R bot /app
COPY --chown=bot . .
USER bot
CMD ["python", "-m", "mehraman_bot.main"]
