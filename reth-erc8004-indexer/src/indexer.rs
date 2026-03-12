use crate::storage::{initial_backfill_range, BlockCursor, EventPayload, Storage, StoredEvent};
use alloy_primitives::{keccak256, Address, B256, U256};
use alloy_sol_types::{sol, sol_data, SolEvent, SolType};
use base64::engine::general_purpose::STANDARD as BASE64_STANDARD;
use base64::Engine;
use eyre::{eyre, WrapErr};
use futures::TryStreamExt;
use reth::api::{BlockBody, NodeTypes};
use reth::chainspec::EthChainSpec;
use reth::primitives::EthPrimitives;
use reth_execution_types::Chain;
use reth_exex::{BackfillJobFactory, ExExContext};
use reth_node_api::FullNodeComponents;
use serde_json::Value;

type MetadataEventData = (sol_data::String, sol_data::Bytes);

sol! {
    event Registered(uint256 indexed agentId, string agentURI, address indexed owner);
    event URIUpdated(uint256 indexed agentId, string newURI, address indexed updatedBy);
}

pub async fn exex_init<Node>(
    ctx: ExExContext<Node>,
    storage: Storage,
    http_client: reqwest::Client,
    registry_address: Address,
    backfill_from_block: u64,
) -> eyre::Result<impl std::future::Future<Output = eyre::Result<()>>>
where
    Node: FullNodeComponents<Types: NodeTypes<Primitives = EthPrimitives>>,
{
    storage.init()?;
    Ok(run_indexer(
        ctx,
        storage,
        http_client,
        registry_address,
        backfill_from_block,
    ))
}

async fn run_indexer<Node>(
    mut ctx: ExExContext<Node>,
    storage: Storage,
    http_client: reqwest::Client,
    registry_address: Address,
    backfill_from_block: u64,
) -> eyre::Result<()>
where
    Node: FullNodeComponents<Types: NodeTypes<Primitives = EthPrimitives>>,
{
    let chain_id = ctx.config.chain.chain().id();

    if let Some(range) = initial_backfill_range(
        storage.current_cursor()?,
        ctx.head.number,
        backfill_from_block,
    ) {
        let job_factory = BackfillJobFactory::new(ctx.evm_config().clone(), ctx.provider().clone());
        let mut stream = job_factory.backfill(range).into_stream();
        while let Some(chain) = stream.try_next().await? {
            process_committed_chain(chain_id, registry_address, &storage, &http_client, &chain)
                .await?;
            ctx.send_finished_height(chain.tip().num_hash())?;
        }
    } else if storage.current_cursor()?.is_none() {
        storage.apply_events(
            chain_id,
            &BlockCursor {
                block_number: ctx.head.number,
                block_hash: ctx.head.hash.to_string(),
                block_timestamp: time::OffsetDateTime::now_utc().unix_timestamp(),
            },
            true,
            &[],
        )?;
    }

    while let Some(notification) = ctx.notifications.try_next().await? {
        if let Some(reverted_chain) = notification.reverted_chain() {
            process_reverted_chain(chain_id, registry_address, &storage, &reverted_chain)?;
        }

        if let Some(committed_chain) = notification.committed_chain() {
            process_committed_chain(
                chain_id,
                registry_address,
                &storage,
                &http_client,
                &committed_chain,
            )
            .await?;
            ctx.send_finished_height(committed_chain.tip().num_hash())?;
        }
    }

    Ok(())
}

async fn process_committed_chain(
    chain_id: u64,
    registry_address: Address,
    storage: &Storage,
    http_client: &reqwest::Client,
    chain: &Chain,
) -> eyre::Result<()> {
    let mut events = Vec::new();

    for (block, receipts) in chain.blocks_and_receipts() {
        for (tx_index, (tx, receipt)) in block
            .body()
            .transactions_iter()
            .zip(receipts.iter())
            .enumerate()
        {
            for (log_index, log) in receipt.logs.iter().enumerate() {
                if log.address != registry_address {
                    continue;
                }

                if let Some(event) = decode_log(
                    chain_id,
                    block.num_hash().number,
                    block.hash(),
                    block.timestamp as i64,
                    tx.hash().to_string(),
                    tx_index as u32,
                    log_index as u32,
                    log.topics(),
                    &log.data.data,
                    http_client,
                )
                .await?
                {
                    events.push(event);
                }
            }
        }
    }

    storage.apply_events(
        chain_id,
        &BlockCursor {
            block_number: chain.tip().num_hash().number,
            block_hash: chain.tip().hash().to_string(),
            block_timestamp: chain.tip().timestamp as i64,
        },
        true,
        &events,
    )?;

    Ok(())
}

fn process_reverted_chain(
    chain_id: u64,
    registry_address: Address,
    storage: &Storage,
    chain: &Chain,
) -> eyre::Result<()> {
    let mut events = Vec::new();
    for (block, receipts) in chain.blocks_and_receipts() {
        for (tx_index, (tx, receipt)) in block
            .body()
            .transactions_iter()
            .zip(receipts.iter())
            .enumerate()
        {
            for (log_index, log) in receipt.logs.iter().enumerate() {
                if log.address != registry_address {
                    continue;
                }

                if let Some(event) = decode_log_sync(
                    chain_id,
                    block.num_hash().number,
                    block.hash(),
                    block.timestamp as i64,
                    tx.hash().to_string(),
                    tx_index as u32,
                    log_index as u32,
                    log.topics(),
                    &log.data.data,
                )? {
                    events.push(event);
                }
            }
        }
    }

    let reverted_to_block = chain.first().number.saturating_sub(1);
    storage.revert_events(chain_id, reverted_to_block, &events)?;
    Ok(())
}

async fn decode_log(
    chain_id: u64,
    block_number: u64,
    block_hash: B256,
    block_timestamp: i64,
    tx_hash: String,
    tx_index: u32,
    log_index: u32,
    topics: &[B256],
    data: &[u8],
    http_client: &reqwest::Client,
) -> eyre::Result<Option<StoredEvent>> {
    let Some(topic0) = topics.first().copied() else {
        return Ok(None);
    };

    if topic0 == Registered::SIGNATURE_HASH {
        let decoded = Registered::decode_raw_log(topics, data)?;
        let (registration_json, fetch_error) =
            fetch_registration_json(http_client, &decoded.agentURI).await;
        return Ok(Some(StoredEvent {
            chain_id,
            token_id: decoded.agentId.to_string(),
            block_number,
            block_hash: block_hash.to_string(),
            block_timestamp,
            tx_hash,
            tx_index,
            log_index,
            payload: EventPayload::Registered {
                owner_address: decoded.owner.to_string(),
                uri: decoded.agentURI,
                registration_json,
                fetch_error,
            },
        }));
    }

    if topic0 == URIUpdated::SIGNATURE_HASH {
        let decoded = URIUpdated::decode_raw_log(topics, data)?;
        let (registration_json, fetch_error) =
            fetch_registration_json(http_client, &decoded.newURI).await;
        return Ok(Some(StoredEvent {
            chain_id,
            token_id: decoded.agentId.to_string(),
            block_number,
            block_hash: block_hash.to_string(),
            block_timestamp,
            tx_hash,
            tx_index,
            log_index,
            payload: EventPayload::UriUpdated {
                updated_by: decoded.updatedBy.to_string(),
                uri: decoded.newURI,
                registration_json,
                fetch_error,
            },
        }));
    }

    decode_log_sync(
        chain_id,
        block_number,
        block_hash,
        block_timestamp,
        tx_hash,
        tx_index,
        log_index,
        topics,
        data,
    )
}

fn decode_log_sync(
    chain_id: u64,
    block_number: u64,
    block_hash: B256,
    block_timestamp: i64,
    tx_hash: String,
    tx_index: u32,
    log_index: u32,
    topics: &[B256],
    data: &[u8],
) -> eyre::Result<Option<StoredEvent>> {
    let Some(topic0) = topics.first().copied() else {
        return Ok(None);
    };

    if topic0 != keccak256("MetadataSet(uint256,string,string,bytes)") {
        return Ok(None);
    }
    if topics.len() < 3 {
        return Err(eyre!("metadata event missing indexed topics"));
    }
    let (metadata_key, metadata_value): (String, alloy_primitives::Bytes) =
        MetadataEventData::abi_decode_params(data)
            .wrap_err("failed to decode metadata event data")?;
    let metadata_value = metadata_value.to_vec();
    let metadata_text = String::from_utf8(metadata_value.clone()).ok();
    Ok(Some(StoredEvent {
        chain_id,
        token_id: U256::from_be_bytes(topics[1].into()).to_string(),
        block_number,
        block_hash: block_hash.to_string(),
        block_timestamp,
        tx_hash,
        tx_index,
        log_index,
        payload: EventPayload::MetadataSet {
            indexed_key_hash: topics[2].to_string(),
            metadata_key,
            metadata_value,
            metadata_text,
        },
    }))
}

async fn fetch_registration_json(
    http_client: &reqwest::Client,
    uri: &str,
) -> (Option<Value>, Option<String>) {
    if uri.is_empty() {
        return (None, Some("empty URI".to_owned()));
    }

    if let Some(encoded) = uri.strip_prefix("data:application/json;base64,") {
        match BASE64_STANDARD.decode(encoded) {
            Ok(decoded) => match serde_json::from_slice::<Value>(&decoded) {
                Ok(value) => return (Some(value), None),
                Err(error) => return (None, Some(format!("invalid data URI JSON: {error}"))),
            },
            Err(error) => return (None, Some(format!("invalid data URI base64: {error}"))),
        }
    }

    if !uri.starts_with("http://") && !uri.starts_with("https://") {
        return (None, Some("unsupported URI scheme".to_owned()));
    }

    match http_client.get(uri).send().await {
        Ok(response) => match response.error_for_status() {
            Ok(ok_response) => match ok_response.json::<Value>().await {
                Ok(value) => (Some(value), None),
                Err(error) => (None, Some(format!("invalid JSON at URI: {error}"))),
            },
            Err(error) => (None, Some(format!("registration fetch failed: {error}"))),
        },
        Err(error) => (None, Some(format!("registration fetch failed: {error}"))),
    }
}

pub fn build_http_client(timeout_secs: u64) -> eyre::Result<reqwest::Client> {
    reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(timeout_secs))
        .build()
        .wrap_err("failed to build reqwest client")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn fetch_registration_supports_data_uris() {
        let client = build_http_client(5).expect("client");
        let uri = format!(
            "data:application/json;base64,{}",
            BASE64_STANDARD.encode(r#"{"name":"alpha"}"#)
        );
        let (registration, error) = fetch_registration_json(&client, &uri).await;
        assert!(error.is_none());
        assert_eq!(registration.expect("registration")["name"], "alpha");
    }

    #[test]
    fn decode_metadata_set_captures_utf8_values() {
        let topics = vec![
            keccak256("MetadataSet(uint256,string,string,bytes)"),
            B256::from(U256::from(7).to_be_bytes()),
            B256::ZERO,
        ];
        let data = MetadataEventData::abi_encode_params(&(
            "metadata.best_val_bpb".to_owned(),
            b"1.234".to_vec(),
        ));
        let decoded = decode_log_sync(
            84532,
            10,
            B256::ZERO,
            1_710_000_000,
            "0xtx".to_owned(),
            0,
            0,
            &topics,
            &data,
        )
        .expect("decode")
        .expect("event");

        match decoded.payload {
            EventPayload::MetadataSet {
                metadata_key,
                metadata_text,
                ..
            } => {
                assert_eq!(metadata_key, "metadata.best_val_bpb");
                assert_eq!(metadata_text.as_deref(), Some("1.234"));
            }
            other => panic!("unexpected payload: {other:?}"),
        }
    }
}
