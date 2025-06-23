# Uniswap V2 In-Memory Indexing System

A high-performance, resilient, and secure Go service for tracking Uniswap V2-style liquidity pools in real-time. At its core, the package processes a live stream of blockchain blocks to maintain an in-memory, thread-safe registry of vetted pools and their current reserves. This provides consuming applications with a consistently up-to-date, queryable snapshot of on-chain liquidity, making it a robust component for DeFi analytics platforms, monitoring tools, or trading bots.

This is not just a simple log parser; it is a production-grade engine built with a strong emphasis on security, scalability, and operational resilience.

---

## Core Features

* **Secure by Design:** Does not blindly trust any contract. Pools are only initialized if they originate from a curated, whitelisted list of trusted DEX factories, preventing interaction with malicious or non-standard contracts.
* **Scalable & Resilient:** Utilizes an asynchronous, batch-based architecture to initialize new pools. This decouples slow network I/O from the main block processing loop, ensuring the system can never fall behind the head of the chain, even during periods of intense on-chain activity.
* **Self-Healing:** A background reconciliation loop periodically re-validates the state of all tracked pools against the blockchain. This makes the system fault-tolerant, automatically correcting any state drift caused by missed events or temporary node issues.
* **Highly Observable:** Exposes a comprehensive set of Prometheus metrics for monitoring system health, performance, and data integrity. This includes critical metrics like block processing lag, initialization queue depth, and error rates by type.
* **High-Performance:** Employs a data-oriented design for the in-memory registry, ensuring CPU-cache efficiency for fast data access. Read-only views of the entire system state are provided through a lock-free `atomic.Pointer`, preventing readers from ever blocking writers.
* **Extensible:** All external dependencies (ETH client, pool initializer, log parsers, etc.) are injected, making the system highly testable and easy to integrate into a larger infrastructure.

## Architectural Overview

The system is composed of several key components with a clear separation of concerns:

* **`UniswapV2System`:** The central orchestrator. It manages the main event loop, handles concurrency with a `sync.RWMutex`, and coordinates all background processes (initializer, pruner, reconciler).
* **`PoolInitializer`:** A configurable "gatekeeper" responsible for safely initializing new pools. It is configured with a list of `KnownFactory` addresses and their associated fees.
* **`UniswapV2Registry`:** A highly efficient, data-oriented in-memory database that stores the state of all tracked pools.
* **`reserves`, `logs`, `errors`, `metrics`:** Supporting packages that provide specialized, decoupled functionality.

### Operational Model

The system is designed to be a core engine that operates under two key assumptions, with responsibilities cleanly separated between this engine and its surrounding infrastructure:

1.  **A Reliable Block Stream is Provided:** The system assumes it is fed a complete, ordered, and gap-free stream of blocks from an upstream "Block Provider." That provider is responsible for handling node connections, persistence, and historical catch-up logic.
2.  **Processing Speed is Decoupled from Block Time:** The system solves the challenge of keeping pace with the chain by separating work into a "fast path" (synchronous reserve updates) and a "slow path" (asynchronous, batch-based initialization of new pools), ensuring it can never fall behind.

## Key Design Decisions (The "Why")

This project's value comes from a few key architectural decisions:

#### 1. Why use a Factory Whitelist? (Security)

We **explicitly reject** pools that do not originate from a known, trusted factory address. This is the cornerstone of the system's security. It prevents our service from interacting with malicious contracts that mimic the Uniswap V2 interface or non-standard pools with unpredictable internal logic (e.g., fee-on-transfer tokens). By verifying a pool's origin, we can be certain of its internal logic.

#### 2. Why use an Asynchronous Initializer? (Scalability)

A block can contain thousands of new pool creation events. Initializing these synchronously would require thousands of slow network calls, causing the system to stall and fall behind the chain. By placing new pools in a queue and processing them periodically in a background batch, we ensure the main block processing loop remains fast and responsive, guaranteeing the system always keeps up with the chain's head.

#### 3. Why use a State Reconciler? (Resilience)

Any event-based system is vulnerable to missing an event. The state reconciler is a self-healing mechanism that runs periodically, re-fetching the on-chain state for all known pools and correcting any discrepancies. This ensures that even if events are missed, the system's state will always converge to the source of truth.

## Usage Example

```go
package main

import (
    "context"
    "time"

    "[github.com/Iwinswap/iwinswap-uniswap-v2-system/uniswapv2](https://github.com/Iwinswap/iwinswap-uniswap-v2-system/uniswapv2)"
    "[github.com/Iwinswap/iwinswap-uniswap-v2-system/initializer](https://github.com/Iwinswap/iwinswap-uniswap-v2-system/initializer)"
    "[github.com/Iwinswap/iwinswap-uniswap-v2-system/logs](https://github.com/Iwinswap/iwinswap-uniswap-v2-system/logs)"
    "[github.com/Iwinswap/iwinswap-uniswap-v2-system/reserves](https://github.com/Iwinswap/iwinswap-uniswap-v2-system/reserves)"
    "[github.com/prometheus/client_golang/prometheus](https://github.com/prometheus/client_golang/prometheus)"
    "[github.com/ethereum/go-ethereum/common](https://github.com/ethereum/go-ethereum/common)"
)

func main() {
    // 1. Setup dependencies and configuration
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Example Known Factories for Ethereum Mainnet
    knownFactories := []initializer.KnownFactory{
        {
            Address:      common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"),
            ProtocolName: "Uniswap-V2",
            FeeBps:       30,
        },
        // ... add other factories for SushiSwap, etc.
    }
    
    // Create the initializer with our trusted factory list
    poolInit := initializer.NewPoolInitializer(knownFactories)

    // Setup other dependencies (omitted for brevity)
    // var getClient func() (ethclients.ETHClient, error)
    // var newBlockEventer chan *types.Block
    // var inBlockedList func(poolAddr common.Address) bool
    // var errorHandler func(err error)
    // ... etc.

    // 2. Instantiate the system
    system, err := uniswapv2.NewUniswapV2System(
        ctx,
        "ethereum_mainnet_v2",
        prometheus.NewRegistry(),
        newBlockEventer,
        getClient,
        inBlockedList,
        poolInit.Initialize, // Inject the initializer's method
        logs.DiscoverPools,
        logs.UpdatedInBlock,
        reserves.GetReserves, // Inject the reserves fetcher
        // ... other persistence functions
        errorHandler,
        logs.SwapEventInBloom,
        1*time.Minute,  // pruneFrequency
        10*time.Second, // initFrequency
        5*time.Minute,  // resyncFrequency
    )
    if err != nil {
        panic(err)
    }

    // 3. The system is now running its background processes.
    // You can now access its state via the View() method or an API layer.
    
    // ... (run forever or until shutdown signal)
}
```

## Metrics

| Metric                                             | Description                                                                                                          |
|----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------|
| `uniswap_system_last_processed_block`              | **Heartbeat** — should continuously increase.                                                                        |
| `uniswap_system_errors_total`                      | Counter of all errors, labeled by type (e.g., `pool_initialization`, `critical_consistency`).                        |
| `uniswap_system_pending_initialization_queue_size` | Backlog of uninitialized pools. If rising, the initializer may be saturated.                                         |
| `uniswap_system_pools_in_registry_total`           | Total number of pools currently tracked.                                                                             |

---

## Future Development

- **Uniswap V3 Engine** — concentrated liquidity, multiple fee tiers  
- **Curve & Balancer Engines** — modules tailored to their unique AMM logic  
- **API Layer** — public endpoints to serve indexed data  
- **Reliable Block Provider** — service ensuring gap-free block streams and re-org handling
