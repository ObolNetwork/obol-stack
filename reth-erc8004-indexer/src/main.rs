use alloy_primitives::Address;
use clap::{Args, Parser};
use eyre::{eyre, WrapErr};
use reth_erc8004_indexer::{api, indexer, storage::Storage};
use reth_ethereum_cli::chainspec::EthereumChainSpecParser;
use reth_node_ethereum::EthereumNode;
use std::{net::SocketAddr, path::PathBuf};

#[derive(Debug, Clone, Args)]
struct IndexerArgs {
    /// Address for the embedded ERC-8004 indexer HTTP server.
    #[arg(long = "erc8004-indexer-http-addr", default_value = "0.0.0.0:8088")]
    erc8004_indexer_http_addr: String,

    /// SQLite database path used by the embedded indexer.
    #[arg(
        long = "erc8004-indexer-db-path",
        default_value = "/data/erc8004-indexer/indexer.db"
    )]
    erc8004_indexer_db_path: PathBuf,

    /// ERC-8004 identity registry contract address to index.
    #[arg(
        long = "erc8004-registry-address",
        default_value = "0x8004A818BFB912233c491871b3d84c89A494BD9e"
    )]
    erc8004_registry_address: String,

    /// Historical block to begin the initial backfill from when no cursor exists.
    #[arg(long = "erc8004-backfill-from-block", default_value_t = 0)]
    erc8004_backfill_from_block: u64,

    /// Timeout for offchain registration fetches.
    #[arg(long = "erc8004-http-timeout-seconds", default_value_t = 15)]
    erc8004_http_timeout_seconds: u64,
}

fn main() -> eyre::Result<()> {
    reth::cli::Cli::<EthereumChainSpecParser, IndexerArgs>::parse().run(|builder, args| async move {
        let listen_addr: SocketAddr = args
            .erc8004_indexer_http_addr
            .parse()
            .wrap_err("invalid --erc8004-indexer-http-addr")?;
        let registry_address: Address = args
            .erc8004_registry_address
            .parse()
            .wrap_err("invalid --erc8004-registry-address")?;
        let storage = Storage::new(
            args.erc8004_indexer_db_path.clone(),
            args.erc8004_registry_address.clone(),
        );
        storage.init()?;

        let http_client = indexer::build_http_client(args.erc8004_http_timeout_seconds)?;
        let api_storage = storage.clone();
        let mut api_server = tokio::spawn(async move {
            let listener = tokio::net::TcpListener::bind(listen_addr)
                .await
                .wrap_err("failed to bind ERC-8004 indexer HTTP listener")?;
            axum::serve(listener, api::router(api_storage))
                .await
                .wrap_err("ERC-8004 indexer HTTP server stopped unexpectedly")
        });

        let exex_storage = storage.clone();
        let exex_client = http_client.clone();
        let backfill_from_block = args.erc8004_backfill_from_block;

        let handle = builder
            .node(EthereumNode::default())
            .install_exex("ERC8004Indexer", move |ctx| {
                let exex_storage = exex_storage.clone();
                let exex_client = exex_client.clone();
                async move {
                    indexer::exex_init(
                        ctx,
                        exex_storage,
                        exex_client,
                        registry_address,
                        backfill_from_block,
                    )
                    .await
                }
            })
            .launch_with_debug_capabilities()
            .await?;

        tokio::select! {
            node_exit = handle.wait_for_node_exit() => {
                api_server.abort();
                node_exit
            }
            server_exit = &mut api_server => {
                match server_exit {
                    Ok(Ok(())) => Err(eyre!("ERC-8004 indexer HTTP server exited unexpectedly")),
                    Ok(Err(error)) => Err(error.into()),
                    Err(error) if error.is_cancelled() => Ok(()),
                    Err(error) => Err(error.into()),
                }
            }
        }
    })
}
