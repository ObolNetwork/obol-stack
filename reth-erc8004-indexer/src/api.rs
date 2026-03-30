use crate::storage::{ListOptions, Pagination, SearchOptions, Storage};
use axum::{
    extract::{Path, Query, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
    Json, Router,
};
use serde::{Deserialize, Serialize};
use serde_json::json;
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

const API_VERSION: &str = "0.1.0";

#[derive(Debug, Clone)]
pub struct AppState {
    storage: Storage,
}

#[derive(Debug, Deserialize)]
struct AgentsQuery {
    page: Option<u64>,
    limit: Option<u64>,
    #[serde(rename = "chainId")]
    chain_id: Option<u64>,
    protocol: Option<String>,
    search: Option<String>,
    #[serde(rename = "sortBy")]
    sort_by: Option<String>,
    #[serde(rename = "sortOrder")]
    sort_order: Option<String>,
    #[serde(rename = "ownerAddress")]
    owner_address: Option<String>,
}

#[derive(Debug, Deserialize)]
struct SearchQuery {
    #[serde(rename = "q")]
    query: Option<String>,
    limit: Option<u64>,
    #[serde(rename = "chainId")]
    chain_id: Option<u64>,
}

#[derive(Debug, Serialize)]
struct ResponseMeta {
    version: &'static str,
    timestamp: String,
    #[serde(rename = "requestId")]
    request_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pagination: Option<Pagination>,
}

#[derive(Debug, Serialize)]
struct Envelope<T> {
    success: bool,
    data: T,
    meta: ResponseMeta,
}

pub fn router(storage: Storage) -> Router {
    Router::new()
        .route("/health", get(health))
        .route("/api/v1/public/agents", get(list_agents))
        .route("/api/v1/public/agents/search", get(search_agents))
        .route(
            "/api/v1/public/agents/{chain_id}/{token_id}",
            get(get_agent),
        )
        .route("/api/v1/public/stats", get(stats))
        .with_state(AppState { storage })
}

async fn health(State(state): State<AppState>) -> Response {
    match state.storage.health_snapshot() {
        Ok(snapshot) => {
            let status = if snapshot.ready {
                StatusCode::OK
            } else {
                StatusCode::SERVICE_UNAVAILABLE
            };
            (status, Json(snapshot)).into_response()
        }
        Err(error) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_error",
            error.to_string(),
        ),
    }
}

async fn list_agents(State(state): State<AppState>, Query(query): Query<AgentsQuery>) -> Response {
    let options = ListOptions {
        page: query.page.unwrap_or(1),
        limit: query.limit.unwrap_or(20),
        chain_id: query.chain_id,
        protocol: query.protocol,
        search: query.search,
        sort_by: query.sort_by,
        sort_order: query.sort_order,
        owner_address: query.owner_address,
    };

    match state.storage.list_agents(&options) {
        Ok(page) => success_response(page.items, Some(page.pagination)),
        Err(error) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_error",
            error.to_string(),
        ),
    }
}

async fn get_agent(
    State(state): State<AppState>,
    Path((chain_id, token_id)): Path<(u64, String)>,
) -> Response {
    match state.storage.get_agent(chain_id, &token_id) {
        Ok(Some(agent)) => success_response(agent, None),
        Ok(None) => error_response(StatusCode::NOT_FOUND, "not_found", "agent not found"),
        Err(error) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_error",
            error.to_string(),
        ),
    }
}

async fn search_agents(
    State(state): State<AppState>,
    Query(query): Query<SearchQuery>,
) -> Response {
    let Some(search_query) = query.query.map(|value| value.trim().to_owned()) else {
        return error_response(StatusCode::BAD_REQUEST, "missing_query", "q is required");
    };
    if search_query.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "missing_query", "q is required");
    }

    let options = SearchOptions {
        query: search_query,
        limit: query.limit.unwrap_or(20),
        chain_id: query.chain_id,
    };

    match state.storage.search_agents(&options) {
        Ok(items) => {
            let pagination = Pagination {
                page: 1,
                limit: options.limit.clamp(1, 100),
                total: items.len() as u64,
                total_pages: if items.is_empty() { 0 } else { 1 },
            };
            success_response(items, Some(pagination))
        }
        Err(error) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_error",
            error.to_string(),
        ),
    }
}

async fn stats(State(state): State<AppState>) -> Response {
    match state.storage.stats() {
        Ok(snapshot) => success_response(snapshot, None),
        Err(error) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_error",
            error.to_string(),
        ),
    }
}

fn success_response<T: Serialize>(data: T, pagination: Option<Pagination>) -> Response {
    let body = Envelope {
        success: true,
        data,
        meta: response_meta(pagination),
    };
    (StatusCode::OK, Json(body)).into_response()
}

fn error_response(status: StatusCode, code: &'static str, message: impl Into<String>) -> Response {
    let body = json!({
        "success": false,
        "error": {
            "code": code,
            "message": message.into(),
        },
        "meta": response_meta(None),
    });
    (status, Json(body)).into_response()
}

fn response_meta(pagination: Option<Pagination>) -> ResponseMeta {
    ResponseMeta {
        version: API_VERSION,
        timestamp: OffsetDateTime::now_utc()
            .format(&Rfc3339)
            .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_owned()),
        request_id: "reth-erc8004-indexer".to_owned(),
        pagination,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::storage::{BlockCursor, EventPayload, StoredEvent};
    use axum::body::Body;
    use http_body_util::BodyExt;
    use serde_json::Value;
    use tempfile::TempDir;
    use tower::ServiceExt;

    fn temp_storage() -> (TempDir, Storage) {
        let dir = TempDir::new().expect("tempdir");
        let storage = Storage::new(
            dir.path().join("indexer.db"),
            "0x8004a818bfb912233c491871b3d84c89a494bd9e",
        );
        storage.init().expect("storage init");
        (dir, storage)
    }

    fn seed(storage: &Storage) {
        let registration = json!({
            "name": "GPU Worker Alpha",
            "description": "A search-friendly worker",
            "image": "https://worker.example.com/alpha.png",
            "active": true,
            "x402Support": true,
            "services": [
                {"name": "web", "endpoint": "https://worker.example.com/services/autoresearch-worker"},
                {"name": "OASF", "skills": ["machine_learning/model_optimization"], "domains": ["technology/artificial_intelligence/research"]}
            ]
        });
        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 22,
                    block_hash: "0xblock22".to_owned(),
                    block_timestamp: 1_710_000_222,
                },
                true,
                &[StoredEvent {
                    chain_id: 84532,
                    token_id: "1".to_owned(),
                    block_number: 22,
                    block_hash: "0xblock22".to_owned(),
                    block_timestamp: 1_710_000_222,
                    tx_hash: "0xtx22".to_owned(),
                    tx_index: 0,
                    log_index: 0,
                    payload: EventPayload::Registered {
                        owner_address: "0x1111111111111111111111111111111111111111".to_owned(),
                        uri: "https://worker.example.com/.well-known/agent-registration.json"
                            .to_owned(),
                        registration_json: Some(registration),
                        fetch_error: None,
                    },
                }],
            )
            .expect("seed storage");
    }

    async fn json_response(response: Response) -> Value {
        let body = response
            .into_body()
            .collect()
            .await
            .expect("body")
            .to_bytes();
        serde_json::from_slice(&body).expect("json body")
    }

    #[tokio::test]
    async fn health_reports_starting_before_sync() {
        let (_dir, storage) = temp_storage();
        let app = router(storage);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/health")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        let body = json_response(response).await;
        assert_eq!(body["status"], "starting");
    }

    #[tokio::test]
    async fn list_agents_returns_pagination() {
        let (_dir, storage) = temp_storage();
        seed(&storage);
        let app = router(storage);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/api/v1/public/agents?page=1&limit=10&protocol=OASF&search=search-friendly")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let body = json_response(response).await;
        assert_eq!(body["success"], true);
        assert_eq!(body["data"].as_array().expect("items").len(), 1);
        assert_eq!(body["meta"]["pagination"]["total"], 1);
    }

    #[tokio::test]
    async fn agent_detail_returns_raw_metadata() {
        let (_dir, storage) = temp_storage();
        seed(&storage);
        let app = router(storage);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/api/v1/public/agents/84532/1")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let body = json_response(response).await;
        assert_eq!(
            body["data"]["raw_metadata"]["offchain_uri"],
            "https://worker.example.com/.well-known/agent-registration.json"
        );
        assert_eq!(body["data"]["name"], "GPU Worker Alpha");
    }

    #[tokio::test]
    async fn search_agents_is_keyword_only() {
        let (_dir, storage) = temp_storage();
        seed(&storage);
        let app = router(storage);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/api/v1/public/agents/search?q=model_optimization&limit=5")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let body = json_response(response).await;
        assert_eq!(body["data"].as_array().expect("items").len(), 1);
    }

    #[tokio::test]
    async fn stats_reports_agent_totals() {
        let (_dir, storage) = temp_storage();
        seed(&storage);
        let app = router(storage);

        let response = app
            .oneshot(
                axum::http::Request::builder()
                    .uri("/api/v1/public/stats")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let body = json_response(response).await;
        assert_eq!(body["data"]["total_agents"], 1);
        assert_eq!(body["data"]["total_users"], 1);
    }
}
