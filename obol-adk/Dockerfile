FROM python:3.13-slim
WORKDIR /app
ENV PORT=8000

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

RUN adduser --disabled-password --gecos "" obol-agent && \
    chown -R obol-agent:obol-agent /app

COPY . .

USER obol-agent

ENV PATH="/home/obol-agent/.local/bin:$PATH"

CMD ["sh", "-c", "uvicorn main:app --host 0.0.0.0 --port $PORT"]