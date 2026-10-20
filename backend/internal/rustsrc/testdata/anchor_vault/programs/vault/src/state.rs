use anchor_lang::prelude::*;

#[account]
pub struct Vault {
    pub owner: Pubkey,
    pub amount: u64,
    pub cfg: Config,
}

#[account(zero_copy)]
pub struct Big {
    pub data: [u8; 1024],
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Default)]
pub struct Config {
    pub fee_bps: u16,
}

impl Vault {
    pub const LEN: usize = 32 + 8 + 2;
    pub const SEED: &'static [u8] = b"vault";

    pub fn finish(v: &Account<'_, Vault>) -> Result<()> {
        let info = v.to_account_info();
        let raw = info.try_borrow_mut_data()?;
        unsafe {
            let _p = raw.as_ptr();
        }
        Ok(())
    }
}
