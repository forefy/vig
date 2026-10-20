use borsh::{BorshDeserialize, BorshSerialize};

#[derive(BorshSerialize, BorshDeserialize)]
pub enum CounterInstruction {
    Create { space: u64 },
    Increment,
    CloseCounter(u8),
}
