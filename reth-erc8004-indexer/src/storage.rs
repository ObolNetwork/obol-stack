use base64::engine::general_purpose::STANDARD as BASE64_STANDARD;
use base64::Engine;
use eyre::WrapErr;
use rusqlite::{
    params, params_from_iter,
    types::{Value as SqlValue, ValueRef},
    Connection, OptionalExtension, Transaction,
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

const SYNC_SCOPE: &str = "default";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BlockCursor {
    pub block_number: u64,
    pub block_hash: String,
    pub block_timestamp: i64,
}

#[derive(Debug, Clone, PartialEq)]
pub enum EventPayload {
    Registered {
        owner_address: String,
        uri: String,
        registration_json: Option<Value>,
        fetch_error: Option<String>,
    },
    UriUpdated {
        updated_by: String,
        uri: String,
        registration_json: Option<Value>,
        fetch_error: Option<String>,
    },
    MetadataSet {
        indexed_key_hash: String,
        metadata_key: String,
        metadata_value: Vec<u8>,
        metadata_text: Option<String>,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub struct StoredEvent {
    pub chain_id: u64,
    pub token_id: String,
    pub block_number: u64,
    pub block_hash: String,
    pub block_timestamp: i64,
    pub tx_hash: String,
    pub tx_index: u32,
    pub log_index: u32,
    pub payload: EventPayload,
}

#[derive(Debug, Clone)]
pub struct ListOptions {
    pub page: u64,
    pub limit: u64,
    pub chain_id: Option<u64>,
    pub protocol: Option<String>,
    pub search: Option<String>,
    pub sort_by: Option<String>,
    pub sort_order: Option<String>,
    pub owner_address: Option<String>,
}

#[derive(Debug, Clone)]
pub struct SearchOptions {
    pub query: String,
    pub limit: u64,
    pub chain_id: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Pagination {
    pub page: u64,
    pub limit: u64,
    pub total: u64,
    #[serde(rename = "totalPages")]
    pub total_pages: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AgentRecord {
    pub id: String,
    pub agent_id: String,
    pub token_id: String,
    pub chain_id: u64,
    pub name: String,
    pub description: String,
    pub image_url: String,
    pub owner_address: String,
    pub uri: String,
    pub active: bool,
    pub x402_supported: bool,
    pub supported_protocols: Vec<String>,
    pub oasf_skills: Vec<String>,
    pub oasf_domains: Vec<String>,
    pub services: Value,
    pub selected_metadata: Value,
    pub raw_metadata: Value,
    pub registration_json: Value,
    pub total_score: f64,
    pub star_count: u64,
    pub total_feedbacks: u64,
    pub created_at: String,
    pub updated_at: String,
    pub last_indexed_block: u64,
    pub last_indexed_at: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AgentPage {
    pub items: Vec<AgentRecord>,
    pub pagination: Pagination,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct StatsSnapshot {
    pub total_agents: u64,
    pub total_users: u64,
    pub total_feedbacks: u64,
    pub total_validations: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct HealthSnapshot {
    pub status: String,
    pub ready: bool,
    pub chain_id: Option<u64>,
    pub registry_address: String,
    pub latest_indexed_block: Option<u64>,
    pub latest_indexed_hash: Option<String>,
    pub latest_indexed_at: Option<String>,
    pub backfilled: bool,
    pub last_error: Option<String>,
}

#[derive(Debug, Clone)]
pub struct Storage {
    path: PathBuf,
    registry_address: String,
}

#[derive(Debug, Default)]
struct DerivedAgent {
    owner_address: String,
    uri: String,
    registration_json: Option<Value>,
    fetch_error: Option<String>,
    metadata: BTreeMap<String, Value>,
    created_at: i64,
    updated_at: i64,
    last_indexed_block: u64,
    last_indexed_at: i64,
}

#[derive(Debug, Default)]
struct ParsedRegistration {
    name: String,
    description: String,
    image_url: String,
    active: bool,
    x402_supported: bool,
    supported_protocols: Vec<String>,
    services: Value,
    oasf_skills: Vec<String>,
    oasf_domains: Vec<String>,
}

impl Storage {
    pub fn new(path: impl Into<PathBuf>, registry_address: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            registry_address: registry_address.into().to_lowercase(),
        }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn init(&self) -> eyre::Result<()> {
        if let Some(parent) = self.path.parent() {
            std::fs::create_dir_all(parent)
                .wrap_err_with(|| format!("failed to create {}", parent.display()))?;
        }

        let conn = self.open_connection()?;
        conn.execute_batch(
            "
            CREATE TABLE IF NOT EXISTS agent_events (
                id TEXT PRIMARY KEY,
                chain_id INTEGER NOT NULL,
                token_id TEXT NOT NULL,
                block_number INTEGER NOT NULL,
                block_hash TEXT NOT NULL,
                block_timestamp INTEGER NOT NULL,
                tx_hash TEXT NOT NULL,
                tx_index INTEGER NOT NULL,
                log_index INTEGER NOT NULL,
                event_kind TEXT NOT NULL,
                owner_address TEXT,
                actor_address TEXT,
                uri TEXT,
                registration_json TEXT,
                fetch_error TEXT,
                indexed_key_hash TEXT,
                metadata_key TEXT,
                metadata_value BLOB,
                metadata_text TEXT
            );
            CREATE INDEX IF NOT EXISTS idx_agent_events_chain_token
                ON agent_events (chain_id, token_id, block_number, tx_index, log_index);
            CREATE INDEX IF NOT EXISTS idx_agent_events_chain_block
                ON agent_events (chain_id, block_number);

            CREATE TABLE IF NOT EXISTS agents (
                chain_id INTEGER NOT NULL,
                token_id TEXT NOT NULL,
                agent_id TEXT NOT NULL,
                owner_address TEXT NOT NULL DEFAULT '',
                uri TEXT NOT NULL DEFAULT '',
                name TEXT NOT NULL DEFAULT '',
                description TEXT NOT NULL DEFAULT '',
                image_url TEXT NOT NULL DEFAULT '',
                active INTEGER NOT NULL DEFAULT 0,
                x402_supported INTEGER NOT NULL DEFAULT 0,
                supported_protocols_json TEXT NOT NULL DEFAULT '[]',
                services_json TEXT NOT NULL DEFAULT '[]',
                oasf_skills_json TEXT NOT NULL DEFAULT '[]',
                oasf_domains_json TEXT NOT NULL DEFAULT '[]',
                selected_metadata_json TEXT NOT NULL DEFAULT '{}',
                raw_metadata_json TEXT NOT NULL DEFAULT '{}',
                registration_json TEXT,
                fetch_error TEXT,
                search_text TEXT NOT NULL DEFAULT '',
                created_at INTEGER NOT NULL DEFAULT 0,
                updated_at INTEGER NOT NULL DEFAULT 0,
                last_indexed_block INTEGER NOT NULL DEFAULT 0,
                last_indexed_at INTEGER NOT NULL DEFAULT 0,
                total_score REAL NOT NULL DEFAULT 0,
                star_count INTEGER NOT NULL DEFAULT 0,
                total_feedbacks INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY (chain_id, token_id)
            );

            CREATE TABLE IF NOT EXISTS sync_state (
                scope TEXT PRIMARY KEY,
                chain_id INTEGER,
                registry_address TEXT NOT NULL,
                ready INTEGER NOT NULL DEFAULT 0,
                backfilled INTEGER NOT NULL DEFAULT 0,
                latest_processed_block INTEGER,
                latest_processed_hash TEXT,
                latest_processed_at INTEGER,
                last_error TEXT
            );
            ",
        )?;
        conn.execute(
            "
            INSERT INTO sync_state (
                scope,
                registry_address,
                ready,
                backfilled
            ) VALUES (?1, ?2, 0, 0)
            ON CONFLICT(scope) DO NOTHING
            ",
            params![SYNC_SCOPE, self.registry_address],
        )?;

        Ok(())
    }

    pub fn current_cursor(&self) -> eyre::Result<Option<u64>> {
        let conn = self.open_connection()?;
        let cursor: Option<i64> = conn.query_row(
            "SELECT latest_processed_block FROM sync_state WHERE scope = ?1",
            params![SYNC_SCOPE],
            |row| row.get(0),
        )?;
        Ok(cursor.and_then(|value| u64::try_from(value).ok()))
    }

    pub fn apply_events(
        &self,
        chain_id: u64,
        cursor: &BlockCursor,
        mark_backfilled: bool,
        events: &[StoredEvent],
    ) -> eyre::Result<()> {
        let mut conn = self.open_connection()?;
        let tx = conn.transaction()?;

        let mut affected = BTreeSet::new();
        for event in events {
            affected.insert(event.token_id.clone());
            self.insert_event(&tx, event)?;
        }

        for token_id in affected {
            self.rebuild_agent(&tx, chain_id, &token_id)?;
        }

        tx.execute(
            "
            UPDATE sync_state
               SET chain_id = ?2,
                   ready = 1,
                   backfilled = CASE WHEN ?3 THEN 1 ELSE backfilled END,
                   latest_processed_block = ?4,
                   latest_processed_hash = ?5,
                   latest_processed_at = ?6,
                   last_error = NULL
             WHERE scope = ?1
            ",
            params![
                SYNC_SCOPE,
                chain_id as i64,
                mark_backfilled,
                cursor.block_number as i64,
                cursor.block_hash.as_str(),
                cursor.block_timestamp,
            ],
        )?;
        tx.commit()?;
        Ok(())
    }

    pub fn revert_events(
        &self,
        chain_id: u64,
        reverted_to_block: u64,
        events: &[StoredEvent],
    ) -> eyre::Result<()> {
        let mut conn = self.open_connection()?;
        let tx = conn.transaction()?;
        let mut affected = BTreeSet::new();

        for event in events {
            affected.insert(event.token_id.clone());
            tx.execute(
                "DELETE FROM agent_events WHERE id = ?1",
                params![event.event_id()],
            )?;
        }

        for token_id in affected {
            self.rebuild_agent(&tx, chain_id, &token_id)?;
        }

        tx.execute(
            "
            UPDATE sync_state
               SET chain_id = ?2,
                   ready = 1,
                   latest_processed_block = ?3,
                   latest_processed_hash = NULL,
                   latest_processed_at = ?4,
                   last_error = NULL
             WHERE scope = ?1
            ",
            params![
                SYNC_SCOPE,
                chain_id as i64,
                reverted_to_block as i64,
                now_utc(),
            ],
        )?;
        tx.commit()?;
        Ok(())
    }

    pub fn mark_unhealthy(
        &self,
        chain_id: Option<u64>,
        message: impl Into<String>,
    ) -> eyre::Result<()> {
        let conn = self.open_connection()?;
        conn.execute(
            "
            UPDATE sync_state
               SET chain_id = COALESCE(?2, chain_id),
                   ready = 0,
                   last_error = ?3,
                   latest_processed_at = ?4
             WHERE scope = ?1
            ",
            params![
                SYNC_SCOPE,
                chain_id.map(|value| value as i64),
                message.into(),
                now_utc()
            ],
        )?;
        Ok(())
    }

    pub fn health_snapshot(&self) -> eyre::Result<HealthSnapshot> {
        let conn = self.open_connection()?;
        let snapshot = conn.query_row(
            "
            SELECT
                chain_id,
                registry_address,
                ready,
                backfilled,
                latest_processed_block,
                latest_processed_hash,
                latest_processed_at,
                last_error
            FROM sync_state
            WHERE scope = ?1
            ",
            params![SYNC_SCOPE],
            |row| {
                let chain_id: Option<i64> = row.get(0)?;
                let registry_address: String = row.get(1)?;
                let ready: bool = row.get(2)?;
                let backfilled: bool = row.get(3)?;
                let latest_processed_block: Option<i64> = row.get(4)?;
                let latest_processed_hash: Option<String> = row.get(5)?;
                let latest_processed_at: Option<i64> = row.get(6)?;
                let last_error: Option<String> = row.get(7)?;

                let status = if ready {
                    "ok"
                } else if last_error.is_some() {
                    "unhealthy"
                } else {
                    "starting"
                };

                Ok(HealthSnapshot {
                    status: status.to_owned(),
                    ready,
                    chain_id: chain_id.and_then(|value| u64::try_from(value).ok()),
                    registry_address,
                    latest_indexed_block: latest_processed_block
                        .and_then(|value| u64::try_from(value).ok()),
                    latest_indexed_hash: latest_processed_hash,
                    latest_indexed_at: latest_processed_at.map(format_timestamp),
                    backfilled,
                    last_error,
                })
            },
        )?;
        Ok(snapshot)
    }

    pub fn get_agent(&self, chain_id: u64, token_id: &str) -> eyre::Result<Option<AgentRecord>> {
        let conn = self.open_connection()?;
        self.get_agent_with_conn(&conn, chain_id, token_id)
    }

    pub fn list_agents(&self, options: &ListOptions) -> eyre::Result<AgentPage> {
        let conn = self.open_connection()?;
        let page = options.page.max(1);
        let limit = options.limit.clamp(1, 100);
        let offset = ((page - 1) * limit) as i64;

        let (where_sql, query_params) = build_where_clause(
            options.chain_id,
            options.protocol.as_deref(),
            options.search.as_deref(),
            options.owner_address.as_deref(),
        );
        let count_sql = format!("SELECT COUNT(*) FROM agents WHERE {where_sql}");
        let total: i64 =
            conn.query_row(&count_sql, params_from_iter(query_params.iter()), |row| {
                row.get(0)
            })?;

        let sort_by = sort_expression(options.sort_by.as_deref());
        let sort_order = sort_order(options.sort_order.as_deref());
        let list_sql = format!(
            "
            SELECT
                chain_id,
                token_id,
                agent_id,
                owner_address,
                uri,
                name,
                description,
                image_url,
                active,
                x402_supported,
                supported_protocols_json,
                services_json,
                oasf_skills_json,
                oasf_domains_json,
                selected_metadata_json,
                raw_metadata_json,
                registration_json,
                total_score,
                star_count,
                total_feedbacks,
                created_at,
                updated_at,
                last_indexed_block,
                last_indexed_at
            FROM agents
            WHERE {where_sql}
            ORDER BY {sort_by} {sort_order}
            LIMIT ? OFFSET ?
            "
        );

        let mut params_with_window = query_params.clone();
        params_with_window.push(SqlValue::Integer(limit as i64));
        params_with_window.push(SqlValue::Integer(offset));

        let mut stmt = conn.prepare(&list_sql)?;
        let rows = stmt.query_map(params_from_iter(params_with_window.iter()), |row| {
            map_agent_row(row)
        })?;
        let items = rows.collect::<Result<Vec<_>, _>>()?;

        let total = total.max(0) as u64;
        let total_pages = if total == 0 { 0 } else { total.div_ceil(limit) };
        Ok(AgentPage {
            items,
            pagination: Pagination {
                page,
                limit,
                total,
                total_pages,
            },
        })
    }

    pub fn search_agents(&self, options: &SearchOptions) -> eyre::Result<Vec<AgentRecord>> {
        let conn = self.open_connection()?;
        let query = options.query.trim().to_lowercase();
        if query.is_empty() {
            return Ok(Vec::new());
        }

        let mut sql = "
            SELECT
                chain_id,
                token_id,
                agent_id,
                owner_address,
                uri,
                name,
                description,
                image_url,
                active,
                x402_supported,
                supported_protocols_json,
                services_json,
                oasf_skills_json,
                oasf_domains_json,
                selected_metadata_json,
                raw_metadata_json,
                registration_json,
                total_score,
                star_count,
                total_feedbacks,
                created_at,
                updated_at,
                last_indexed_block,
                last_indexed_at
            FROM agents
            WHERE search_text LIKE ?
        "
        .to_owned();
        let mut params = vec![SqlValue::Text(format!("%{query}%"))];

        if let Some(chain_id) = options.chain_id {
            sql.push_str(" AND chain_id = ?");
            params.push(SqlValue::Integer(chain_id as i64));
        }

        sql.push_str(" ORDER BY updated_at DESC, name ASC LIMIT ?");
        params.push(SqlValue::Integer(options.limit.clamp(1, 100) as i64));

        let mut stmt = conn.prepare(&sql)?;
        let rows = stmt.query_map(params_from_iter(params.iter()), |row| map_agent_row(row))?;
        let items = rows.collect::<Result<Vec<_>, _>>()?;
        Ok(items)
    }

    pub fn stats(&self) -> eyre::Result<StatsSnapshot> {
        let conn = self.open_connection()?;
        let total_agents: i64 =
            conn.query_row("SELECT COUNT(*) FROM agents", [], |row| row.get(0))?;
        let total_users: i64 = conn.query_row(
            "SELECT COUNT(DISTINCT owner_address) FROM agents WHERE owner_address <> ''",
            [],
            |row| row.get(0),
        )?;
        Ok(StatsSnapshot {
            total_agents: total_agents.max(0) as u64,
            total_users: total_users.max(0) as u64,
            total_feedbacks: 0,
            total_validations: 0,
        })
    }

    fn open_connection(&self) -> eyre::Result<Connection> {
        let conn = Connection::open(&self.path)
            .wrap_err_with(|| format!("failed to open {}", self.path.display()))?;
        conn.pragma_update(None, "journal_mode", "WAL")?;
        conn.pragma_update(None, "synchronous", "NORMAL")?;
        conn.pragma_update(None, "foreign_keys", "ON")?;
        conn.busy_timeout(std::time::Duration::from_secs(5))?;
        Ok(conn)
    }

    fn insert_event(&self, tx: &Transaction<'_>, event: &StoredEvent) -> eyre::Result<()> {
        match &event.payload {
            EventPayload::Registered {
                owner_address,
                uri,
                registration_json,
                fetch_error,
            } => {
                tx.execute(
                    "
                    INSERT OR REPLACE INTO agent_events (
                        id, chain_id, token_id, block_number, block_hash, block_timestamp,
                        tx_hash, tx_index, log_index, event_kind, owner_address, uri,
                        registration_json, fetch_error
                    ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, 'registered', ?10, ?11, ?12, ?13)
                    ",
                    params![
                        event.event_id(),
                        event.chain_id as i64,
                        event.token_id.as_str(),
                        event.block_number as i64,
                        event.block_hash.as_str(),
                        event.block_timestamp,
                        event.tx_hash.as_str(),
                        event.tx_index as i64,
                        event.log_index as i64,
                        owner_address,
                        uri,
                        json_to_string(registration_json.as_ref()),
                        fetch_error,
                    ],
                )?;
            }
            EventPayload::UriUpdated {
                updated_by,
                uri,
                registration_json,
                fetch_error,
            } => {
                tx.execute(
                    "
                    INSERT OR REPLACE INTO agent_events (
                        id, chain_id, token_id, block_number, block_hash, block_timestamp,
                        tx_hash, tx_index, log_index, event_kind, actor_address, uri,
                        registration_json, fetch_error
                    ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, 'uri_updated', ?10, ?11, ?12, ?13)
                    ",
                    params![
                        event.event_id(),
                        event.chain_id as i64,
                        event.token_id.as_str(),
                        event.block_number as i64,
                        event.block_hash.as_str(),
                        event.block_timestamp,
                        event.tx_hash.as_str(),
                        event.tx_index as i64,
                        event.log_index as i64,
                        updated_by,
                        uri,
                        json_to_string(registration_json.as_ref()),
                        fetch_error,
                    ],
                )?;
            }
            EventPayload::MetadataSet {
                indexed_key_hash,
                metadata_key,
                metadata_value,
                metadata_text,
            } => {
                tx.execute(
                    "
                    INSERT OR REPLACE INTO agent_events (
                        id, chain_id, token_id, block_number, block_hash, block_timestamp,
                        tx_hash, tx_index, log_index, event_kind, indexed_key_hash,
                        metadata_key, metadata_value, metadata_text
                    ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, 'metadata_set', ?10, ?11, ?12, ?13)
                    ",
                    params![
                        event.event_id(),
                        event.chain_id as i64,
                        event.token_id.as_str(),
                        event.block_number as i64,
                        event.block_hash.as_str(),
                        event.block_timestamp,
                        event.tx_hash.as_str(),
                        event.tx_index as i64,
                        event.log_index as i64,
                        indexed_key_hash,
                        metadata_key,
                        metadata_value,
                        metadata_text,
                    ],
                )?;
            }
        }
        Ok(())
    }

    fn rebuild_agent(
        &self,
        tx: &Transaction<'_>,
        chain_id: u64,
        token_id: &str,
    ) -> eyre::Result<()> {
        let mut stmt = tx.prepare(
            "
            SELECT
                event_kind,
                owner_address,
                uri,
                registration_json,
                fetch_error,
                metadata_key,
                metadata_value,
                metadata_text,
                block_timestamp,
                block_number
            FROM agent_events
            WHERE chain_id = ?1 AND token_id = ?2
            ORDER BY block_number ASC, tx_index ASC, log_index ASC
            ",
        )?;

        let rows = stmt.query_map(params![chain_id as i64, token_id], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, Option<String>>(1)?,
                row.get::<_, Option<String>>(2)?,
                row.get::<_, Option<String>>(3)?,
                row.get::<_, Option<String>>(4)?,
                row.get::<_, Option<String>>(5)?,
                read_blob(row.get_ref(6)?),
                row.get::<_, Option<String>>(7)?,
                row.get::<_, i64>(8)?,
                row.get::<_, i64>(9)?,
            ))
        })?;

        let mut derived = DerivedAgent::default();
        let mut found = false;

        for row in rows {
            let (
                event_kind,
                owner_address,
                uri,
                registration_json,
                fetch_error,
                metadata_key,
                metadata_value,
                metadata_text,
                block_timestamp,
                block_number,
            ) = row?;

            found = true;
            if derived.created_at == 0 {
                derived.created_at = block_timestamp;
            }
            derived.updated_at = block_timestamp;
            derived.last_indexed_at = block_timestamp;
            derived.last_indexed_block = block_number.max(0) as u64;

            match event_kind.as_str() {
                "registered" => {
                    if let Some(value) = owner_address {
                        derived.owner_address = value.to_lowercase();
                    }
                    if let Some(value) = uri {
                        derived.uri = value;
                    }
                    derived.registration_json = parse_optional_json(registration_json)?;
                    derived.fetch_error = fetch_error;
                }
                "uri_updated" => {
                    if let Some(value) = uri {
                        derived.uri = value;
                    }
                    derived.registration_json = parse_optional_json(registration_json)?;
                    derived.fetch_error = fetch_error;
                }
                "metadata_set" => {
                    if let Some(key) = metadata_key {
                        derived
                            .metadata
                            .insert(key, metadata_json_value(metadata_text, &metadata_value));
                    }
                }
                _ => {}
            }
        }

        if !found {
            tx.execute(
                "DELETE FROM agents WHERE chain_id = ?1 AND token_id = ?2",
                params![chain_id as i64, token_id],
            )?;
            return Ok(());
        }

        let parsed = parse_registration(derived.registration_json.as_ref(), &derived.metadata);
        let raw_metadata = json!({
            "offchain_uri": derived.uri,
            "offchain_content": derived.registration_json,
            "onchain_indexed": derived.metadata,
            "fetch_error": derived.fetch_error,
        });
        let search_text = build_search_text(&parsed, &derived.metadata).to_lowercase();

        tx.execute(
            "
            INSERT OR REPLACE INTO agents (
                chain_id,
                token_id,
                agent_id,
                owner_address,
                uri,
                name,
                description,
                image_url,
                active,
                x402_supported,
                supported_protocols_json,
                services_json,
                oasf_skills_json,
                oasf_domains_json,
                selected_metadata_json,
                raw_metadata_json,
                registration_json,
                fetch_error,
                search_text,
                created_at,
                updated_at,
                last_indexed_block,
                last_indexed_at
            ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20, ?21, ?22, ?23)
            ",
            params![
                chain_id as i64,
                token_id,
                format!("{}:{}:{}", chain_id, self.registry_address, token_id),
                derived.owner_address,
                derived.uri,
                parsed.name,
                parsed.description,
                parsed.image_url,
                parsed.active,
                parsed.x402_supported,
                serde_json::to_string(&parsed.supported_protocols)?,
                serde_json::to_string(&parsed.services)?,
                serde_json::to_string(&parsed.oasf_skills)?,
                serde_json::to_string(&parsed.oasf_domains)?,
                serde_json::to_string(&derived.metadata)?,
                serde_json::to_string(&raw_metadata)?,
                json_to_string(derived.registration_json.as_ref()),
                derived.fetch_error,
                search_text,
                derived.created_at,
                derived.updated_at,
                derived.last_indexed_block as i64,
                derived.last_indexed_at,
            ],
        )?;

        Ok(())
    }

    fn get_agent_with_conn(
        &self,
        conn: &Connection,
        chain_id: u64,
        token_id: &str,
    ) -> eyre::Result<Option<AgentRecord>> {
        let mut stmt = conn.prepare(
            "
            SELECT
                chain_id,
                token_id,
                agent_id,
                owner_address,
                uri,
                name,
                description,
                image_url,
                active,
                x402_supported,
                supported_protocols_json,
                services_json,
                oasf_skills_json,
                oasf_domains_json,
                selected_metadata_json,
                raw_metadata_json,
                registration_json,
                total_score,
                star_count,
                total_feedbacks,
                created_at,
                updated_at,
                last_indexed_block,
                last_indexed_at
            FROM agents
            WHERE chain_id = ?1 AND token_id = ?2
            ",
        )?;

        let agent = stmt
            .query_row(params![chain_id as i64, token_id], |row| map_agent_row(row))
            .optional()?;
        Ok(agent)
    }
}

impl StoredEvent {
    pub fn event_id(&self) -> String {
        format!(
            "{}:{}:{}",
            self.block_hash.to_lowercase(),
            self.tx_hash.to_lowercase(),
            self.log_index
        )
    }
}

pub fn initial_backfill_range(
    cursor: Option<u64>,
    current_head: u64,
    from_block: u64,
) -> Option<std::ops::RangeInclusive<u64>> {
    let start = cursor.map_or(from_block, |value| value.saturating_add(1));
    (start <= current_head).then_some(start..=current_head)
}

fn build_where_clause(
    chain_id: Option<u64>,
    protocol: Option<&str>,
    search: Option<&str>,
    owner_address: Option<&str>,
) -> (String, Vec<SqlValue>) {
    let mut clauses = vec!["1 = 1".to_owned()];
    let mut params = Vec::new();

    if let Some(chain_id) = chain_id {
        clauses.push("chain_id = ?".to_owned());
        params.push(SqlValue::Integer(chain_id as i64));
    }

    if let Some(protocol) = protocol {
        clauses.push("lower(supported_protocols_json) LIKE ?".to_owned());
        params.push(SqlValue::Text(format!(
            "%{}%",
            protocol.trim().to_lowercase()
        )));
    }

    if let Some(search) = search {
        clauses.push("search_text LIKE ?".to_owned());
        params.push(SqlValue::Text(format!(
            "%{}%",
            search.trim().to_lowercase()
        )));
    }

    if let Some(owner_address) = owner_address {
        clauses.push("owner_address = ?".to_owned());
        params.push(SqlValue::Text(owner_address.trim().to_lowercase()));
    }

    (clauses.join(" AND "), params)
}

fn sort_expression(sort_by: Option<&str>) -> &'static str {
    match sort_by.unwrap_or("created_at") {
        "name" => "name COLLATE NOCASE",
        "token_id" => "CAST(token_id AS INTEGER)",
        "total_score" => "total_score",
        "stars" => "star_count",
        _ => "created_at",
    }
}

fn sort_order(sort_order: Option<&str>) -> &'static str {
    match sort_order.unwrap_or("desc").to_lowercase().as_str() {
        "asc" => "ASC",
        _ => "DESC",
    }
}

fn map_agent_row(row: &rusqlite::Row<'_>) -> rusqlite::Result<AgentRecord> {
    let chain_id: i64 = row.get(0)?;
    let token_id: String = row.get(1)?;
    let active: bool = row.get(8)?;
    let x402_supported: bool = row.get(9)?;
    let total_score: f64 = row.get(17)?;
    let star_count: i64 = row.get(18)?;
    let total_feedbacks: i64 = row.get(19)?;
    let created_at: i64 = row.get(20)?;
    let updated_at: i64 = row.get(21)?;
    let last_indexed_block: i64 = row.get(22)?;
    let last_indexed_at: i64 = row.get(23)?;

    Ok(AgentRecord {
        id: row.get(2)?,
        agent_id: row.get(2)?,
        token_id,
        chain_id: chain_id.max(0) as u64,
        owner_address: row.get::<_, String>(3)?,
        uri: row.get::<_, String>(4)?,
        name: row.get::<_, String>(5)?,
        description: row.get::<_, String>(6)?,
        image_url: row.get::<_, String>(7)?,
        active,
        x402_supported,
        supported_protocols: parse_json_string_array(row.get::<_, String>(10)?)?,
        services: parse_json_value(row.get::<_, String>(11)?)?,
        oasf_skills: parse_json_string_array(row.get::<_, String>(12)?)?,
        oasf_domains: parse_json_string_array(row.get::<_, String>(13)?)?,
        selected_metadata: parse_json_value(row.get::<_, String>(14)?)?,
        raw_metadata: parse_json_value(row.get::<_, String>(15)?)?,
        registration_json: parse_optional_json(row.get::<_, Option<String>>(16)?)
            .map_err(|error| {
                rusqlite::Error::FromSqlConversionFailure(
                    16,
                    rusqlite::types::Type::Text,
                    Box::new(error),
                )
            })?
            .unwrap_or(Value::Null),
        total_score,
        star_count: star_count.max(0) as u64,
        total_feedbacks: total_feedbacks.max(0) as u64,
        created_at: format_timestamp(created_at),
        updated_at: format_timestamp(updated_at),
        last_indexed_block: last_indexed_block.max(0) as u64,
        last_indexed_at: format_timestamp(last_indexed_at),
    })
}

fn parse_registration(
    registration_json: Option<&Value>,
    metadata: &BTreeMap<String, Value>,
) -> ParsedRegistration {
    let mut parsed = ParsedRegistration {
        active: true,
        services: Value::Array(Vec::new()),
        ..Default::default()
    };

    if let Some(Value::Object(registration)) = registration_json {
        parsed.name = string_field(registration.get("name"));
        parsed.description = string_field(registration.get("description"));
        parsed.image_url = string_field(registration.get("image"));
        parsed.active = registration
            .get("active")
            .and_then(Value::as_bool)
            .unwrap_or(true);
        parsed.x402_supported = registration
            .get("x402Support")
            .and_then(Value::as_bool)
            .unwrap_or(false);

        if let Some(services) = registration.get("services") {
            parsed.services = services.clone();
            if let Some(array) = services.as_array() {
                let mut protocols = BTreeSet::new();
                let mut skills = BTreeSet::new();
                let mut domains = BTreeSet::new();
                for entry in array {
                    let Some(service) = entry.as_object() else {
                        continue;
                    };
                    if let Some(name) = service.get("name").and_then(Value::as_str) {
                        protocols.insert(name.to_owned());
                        if name.eq_ignore_ascii_case("OASF") {
                            collect_string_list(service.get("skills"), &mut skills);
                            collect_string_list(service.get("domains"), &mut domains);
                        }
                    }
                }
                parsed.supported_protocols = protocols.into_iter().collect();
                parsed.oasf_skills = skills.into_iter().collect();
                parsed.oasf_domains = domains.into_iter().collect();
            }
        }
    }

    if let Some(service_type) = metadata.get("service.type").and_then(Value::as_str) {
        if !parsed
            .supported_protocols
            .iter()
            .any(|item| item.eq_ignore_ascii_case(service_type))
        {
            parsed.supported_protocols.push(service_type.to_owned());
        }
    }

    if metadata.get("x402.supported").is_some() {
        parsed.x402_supported = metadata_flag(metadata.get("x402.supported"));
    }

    parsed
}

fn build_search_text(parsed: &ParsedRegistration, metadata: &BTreeMap<String, Value>) -> String {
    let mut parts = vec![
        parsed.name.clone(),
        parsed.description.clone(),
        parsed.image_url.clone(),
    ];
    parts.extend(parsed.supported_protocols.clone());
    parts.extend(parsed.oasf_skills.clone());
    parts.extend(parsed.oasf_domains.clone());
    for value in metadata.values() {
        match value {
            Value::String(text) => parts.push(text.clone()),
            other => parts.push(other.to_string()),
        }
    }
    parts.join(" ")
}

fn collect_string_list(value: Option<&Value>, target: &mut BTreeSet<String>) {
    let Some(Value::Array(values)) = value else {
        return;
    };
    for entry in values {
        if let Some(text) = entry.as_str() {
            if !text.trim().is_empty() {
                target.insert(text.to_owned());
            }
        }
    }
}

fn metadata_flag(value: Option<&Value>) -> bool {
    match value {
        Some(Value::Bool(flag)) => *flag,
        Some(Value::Number(number)) => number.as_u64().unwrap_or(0) > 0,
        Some(Value::String(text)) => {
            let normalized = text.trim().to_lowercase();
            normalized == "1" || normalized == "true" || normalized == "yes"
        }
        Some(Value::Object(object)) => object
            .get("base64")
            .and_then(Value::as_str)
            .and_then(|encoded| BASE64_STANDARD.decode(encoded).ok())
            .is_some_and(|bytes| !bytes.is_empty() && bytes.iter().any(|byte| *byte != 0)),
        _ => false,
    }
}

fn metadata_json_value(metadata_text: Option<String>, metadata_value: &[u8]) -> Value {
    if let Some(text) = metadata_text {
        if text == "\u{1}" || text == "1" {
            return Value::Bool(true);
        }
        if text.eq_ignore_ascii_case("true") {
            return Value::Bool(true);
        }
        return Value::String(text);
    }

    if metadata_value == [1] {
        return Value::Bool(true);
    }

    json!({ "base64": BASE64_STANDARD.encode(metadata_value) })
}

fn read_blob(value: ValueRef<'_>) -> Vec<u8> {
    match value {
        ValueRef::Blob(bytes) => bytes.to_vec(),
        ValueRef::Text(text) => text.to_vec(),
        _ => Vec::new(),
    }
}

fn json_to_string(value: Option<&Value>) -> Option<String> {
    value.and_then(|item| serde_json::to_string(item).ok())
}

fn parse_optional_json(value: Option<String>) -> serde_json::Result<Option<Value>> {
    match value {
        Some(raw) => Ok(Some(serde_json::from_str(&raw)?)),
        None => Ok(None),
    }
}

fn parse_json_value(value: String) -> rusqlite::Result<Value> {
    serde_json::from_str(&value).map_err(|error| {
        rusqlite::Error::FromSqlConversionFailure(0, rusqlite::types::Type::Text, Box::new(error))
    })
}

fn parse_json_string_array(value: String) -> rusqlite::Result<Vec<String>> {
    serde_json::from_str(&value).map_err(|error| {
        rusqlite::Error::FromSqlConversionFailure(0, rusqlite::types::Type::Text, Box::new(error))
    })
}

fn string_field(value: Option<&Value>) -> String {
    value.and_then(Value::as_str).unwrap_or_default().to_owned()
}

fn format_timestamp(timestamp: i64) -> String {
    OffsetDateTime::from_unix_timestamp(timestamp)
        .unwrap_or(OffsetDateTime::UNIX_EPOCH)
        .format(&Rfc3339)
        .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_owned())
}

fn now_utc() -> i64 {
    OffsetDateTime::now_utc().unix_timestamp()
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn temp_storage() -> (TempDir, Storage) {
        let dir = TempDir::new().expect("tempdir");
        let storage = Storage::new(
            dir.path().join("indexer.db"),
            "0x8004a818bfb912233c491871b3d84c89a494bd9e",
        );
        storage.init().expect("storage init");
        (dir, storage)
    }

    fn registration(name: &str, endpoint: &str) -> Value {
        json!({
            "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
            "name": name,
            "description": format!("{name} description"),
            "image": format!("https://example.com/{name}.png"),
            "active": true,
            "x402Support": true,
            "services": [
                {"name": "web", "endpoint": endpoint, "version": "1.0.0"},
                {"name": "OASF", "version": "0.8", "skills": ["machine_learning/model_optimization"], "domains": ["technology/artificial_intelligence/research"]}
            ]
        })
    }

    fn registered_event(token_id: &str, uri: &str, ts: i64, registration: Value) -> StoredEvent {
        StoredEvent {
            chain_id: 84532,
            token_id: token_id.to_owned(),
            block_number: 10,
            block_hash: "0xblock10".to_owned(),
            block_timestamp: ts,
            tx_hash: "0xtx10".to_owned(),
            tx_index: 0,
            log_index: 0,
            payload: EventPayload::Registered {
                owner_address: "0x1111111111111111111111111111111111111111".to_owned(),
                uri: uri.to_owned(),
                registration_json: Some(registration),
                fetch_error: None,
            },
        }
    }

    fn uri_updated_event(token_id: &str, uri: &str, ts: i64, registration: Value) -> StoredEvent {
        StoredEvent {
            chain_id: 84532,
            token_id: token_id.to_owned(),
            block_number: 11,
            block_hash: "0xblock11".to_owned(),
            block_timestamp: ts,
            tx_hash: "0xtx11".to_owned(),
            tx_index: 0,
            log_index: 1,
            payload: EventPayload::UriUpdated {
                updated_by: "0x2222222222222222222222222222222222222222".to_owned(),
                uri: uri.to_owned(),
                registration_json: Some(registration),
                fetch_error: None,
            },
        }
    }

    fn metadata_event(token_id: &str, key: &str, value: &[u8], text: Option<&str>) -> StoredEvent {
        StoredEvent {
            chain_id: 84532,
            token_id: token_id.to_owned(),
            block_number: 12,
            block_hash: "0xblock12".to_owned(),
            block_timestamp: 1_710_000_222,
            tx_hash: "0xtx12".to_owned(),
            tx_index: 0,
            log_index: 2,
            payload: EventPayload::MetadataSet {
                indexed_key_hash: "0xhash".to_owned(),
                metadata_key: key.to_owned(),
                metadata_value: value.to_vec(),
                metadata_text: text.map(ToOwned::to_owned),
            },
        }
    }

    #[test]
    fn apply_commit_builds_agent_summary() {
        let (_dir, storage) = temp_storage();
        let event = registered_event(
            "1",
            "https://worker.example.com/.well-known/agent-registration.json",
            1_710_000_000,
            registration(
                "GPU Worker Alpha",
                "https://worker.example.com/services/autoresearch-worker",
            ),
        );
        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 10,
                    block_hash: "0xblock10".to_owned(),
                    block_timestamp: 1_710_000_000,
                },
                true,
                &[event],
            )
            .expect("apply events");

        let agent = storage
            .get_agent(84532, "1")
            .expect("get agent")
            .expect("agent");
        assert_eq!(agent.name, "GPU Worker Alpha");
        assert!(agent.x402_supported);
        assert_eq!(
            agent.supported_protocols,
            vec!["OASF".to_owned(), "web".to_owned()]
        );
        assert_eq!(
            agent.oasf_skills,
            vec!["machine_learning/model_optimization".to_owned()]
        );
    }

    #[test]
    fn reorg_rollback_restores_previous_uri() {
        let (_dir, storage) = temp_storage();
        let base = registered_event(
            "1",
            "https://worker.example.com/.well-known/agent-registration.json",
            1_710_000_000,
            registration(
                "GPU Worker Alpha",
                "https://worker.example.com/services/autoresearch-worker",
            ),
        );
        let updated = uri_updated_event(
            "1",
            "https://worker.example.com/v2/agent-registration.json",
            1_710_000_111,
            registration(
                "GPU Worker Beta",
                "https://worker.example.com/services/autoresearch-worker-v2",
            ),
        );

        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 11,
                    block_hash: "0xblock11".to_owned(),
                    block_timestamp: 1_710_000_111,
                },
                true,
                &[base.clone(), updated.clone()],
            )
            .expect("apply events");
        assert_eq!(
            storage
                .get_agent(84532, "1")
                .expect("get agent")
                .expect("agent")
                .name,
            "GPU Worker Beta"
        );

        storage
            .revert_events(84532, 10, &[updated])
            .expect("revert");
        let agent = storage
            .get_agent(84532, "1")
            .expect("get agent")
            .expect("agent");
        assert_eq!(agent.name, "GPU Worker Alpha");
        assert_eq!(
            agent.uri,
            "https://worker.example.com/.well-known/agent-registration.json"
        );
    }

    #[test]
    fn metadata_updates_are_projected_into_agent_state() {
        let (_dir, storage) = temp_storage();
        let base = registered_event(
            "1",
            "https://worker.example.com/.well-known/agent-registration.json",
            1_710_000_000,
            registration(
                "GPU Worker Alpha",
                "https://worker.example.com/services/autoresearch-worker",
            ),
        );
        let metadata = metadata_event("1", "metadata.best_val_bpb", b"1.111", Some("1.111"));

        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 12,
                    block_hash: "0xblock12".to_owned(),
                    block_timestamp: 1_710_000_222,
                },
                true,
                &[base, metadata],
            )
            .expect("apply events");

        let agent = storage
            .get_agent(84532, "1")
            .expect("get agent")
            .expect("agent");
        assert_eq!(agent.selected_metadata["metadata.best_val_bpb"], "1.111");
        assert_eq!(
            agent.raw_metadata["onchain_indexed"]["metadata.best_val_bpb"],
            "1.111"
        );
    }

    #[test]
    fn list_agents_supports_filters_and_search() {
        let (_dir, storage) = temp_storage();
        let first = registered_event(
            "1",
            "https://worker-a.example.com/.well-known/agent-registration.json",
            1_710_000_000,
            registration(
                "GPU Worker Alpha",
                "https://worker-a.example.com/services/autoresearch-worker",
            ),
        );
        let second = StoredEvent {
            token_id: "2".to_owned(),
            tx_hash: "0xtx20".to_owned(),
            block_hash: "0xblock20".to_owned(),
            block_number: 20,
            log_index: 0,
            block_timestamp: 1_710_000_500,
            tx_index: 0,
            chain_id: 84532,
            payload: EventPayload::Registered {
                owner_address: "0x9999999999999999999999999999999999999999".to_owned(),
                uri: "https://worker-b.example.com/.well-known/agent-registration.json".to_owned(),
                registration_json: Some(registration(
                    "Searchable Beta",
                    "https://worker-b.example.com/services/reviewer",
                )),
                fetch_error: None,
            },
        };

        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 20,
                    block_hash: "0xblock20".to_owned(),
                    block_timestamp: 1_710_000_500,
                },
                true,
                &[first, second],
            )
            .expect("apply events");

        let filtered = storage
            .list_agents(&ListOptions {
                page: 1,
                limit: 10,
                chain_id: Some(84532),
                protocol: Some("OASF".to_owned()),
                search: Some("searchable".to_owned()),
                sort_by: Some("name".to_owned()),
                sort_order: Some("asc".to_owned()),
                owner_address: None,
            })
            .expect("list agents");

        assert_eq!(filtered.items.len(), 1);
        assert_eq!(filtered.items[0].name, "Searchable Beta");
        assert_eq!(filtered.pagination.total, 1);
    }

    #[test]
    fn health_defaults_to_starting_and_becomes_ready() {
        let (_dir, storage) = temp_storage();
        assert_eq!(
            storage.health_snapshot().expect("health").status,
            "starting"
        );

        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 10,
                    block_hash: "0xblock10".to_owned(),
                    block_timestamp: 1_710_000_000,
                },
                true,
                &[registered_event(
                    "1",
                    "https://worker.example.com/.well-known/agent-registration.json",
                    1_710_000_000,
                    registration(
                        "GPU Worker Alpha",
                        "https://worker.example.com/services/autoresearch-worker",
                    ),
                )],
            )
            .expect("apply events");

        let health = storage.health_snapshot().expect("health");
        assert_eq!(health.status, "ok");
        assert!(health.ready);
        assert_eq!(health.latest_indexed_block, Some(10));
    }

    #[test]
    fn current_cursor_persists_across_restarts() {
        let (dir, storage) = temp_storage();
        storage
            .apply_events(
                84532,
                &BlockCursor {
                    block_number: 55,
                    block_hash: "0xblock55".to_owned(),
                    block_timestamp: 1_710_000_777,
                },
                true,
                &[],
            )
            .expect("apply cursor");

        let reopened = Storage::new(
            dir.path().join("indexer.db"),
            "0x8004a818bfb912233c491871b3d84c89a494bd9e",
        );
        reopened.init().expect("reopen init");
        assert_eq!(reopened.current_cursor().expect("cursor"), Some(55));
    }

    #[test]
    fn backfill_range_respects_existing_cursor() {
        assert_eq!(initial_backfill_range(None, 25, 5), Some(5..=25));
        assert_eq!(initial_backfill_range(Some(24), 25, 5), Some(25..=25));
        assert_eq!(initial_backfill_range(Some(25), 25, 5), None);
    }
}
