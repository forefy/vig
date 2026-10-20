// Solana libraries expect a Node Buffer global. Imported before anything else.
import { Buffer } from 'buffer'
;(globalThis as unknown as { Buffer: typeof Buffer }).Buffer = Buffer
