//! Helpers shared by programs. Not a program.
pub fn checked_fee(amount: u64, bps: u64) -> u64 {
    amount.checked_mul(bps).unwrap().checked_div(10_000).unwrap()
}
