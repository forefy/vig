use crate::instruction::CounterInstruction;
use borsh::BorshDeserialize;
use solana_program::{account_info::{next_account_info, AccountInfo}, entrypoint::ProgramResult, program::{invoke, invoke_signed}, pubkey::Pubkey, system_instruction};

pub struct Processor;

impl Processor {
    pub fn process(program_id: &Pubkey, accounts: &[AccountInfo], data: &[u8]) -> ProgramResult {
        let ix = CounterInstruction::try_from_slice(data)?;
        match ix {
            CounterInstruction::Create { space } => Self::create(program_id, accounts, space),
            CounterInstruction::Increment => Self::increment(accounts),
            CounterInstruction::CloseCounter(_) => {
                close(accounts)
            }
        }
    }

    fn create(program_id: &Pubkey, accounts: &[AccountInfo], space: u64) -> ProgramResult {
        let iter = &mut accounts.iter();
        let payer_info = next_account_info(iter)?;
        let counter_info = next_account_info(iter)?;
        if !payer_info.is_signer {
            return Err(solana_program::program_error::ProgramError::MissingRequiredSignature);
        }
        let (pda, bump) = Pubkey::find_program_address(&[b"counter", payer_info.key.as_ref()], program_id);
        let ix = system_instruction::create_account(payer_info.key, &pda, 1, space, program_id);
        invoke_signed(&ix, &[payer_info.clone(), counter_info.clone()], &[&[b"counter", payer_info.key.as_ref(), &[bump]]])?;
        Ok(())
    }

    fn increment(accounts: &[AccountInfo]) -> ProgramResult {
        let iter = &mut accounts.iter();
        let counter_info = next_account_info(iter)?;
        let mut data = counter_info.try_borrow_mut_data()?;
        data[0] += 1;
        Ok(())
    }
}

pub fn close(accounts: &[AccountInfo]) -> ProgramResult {
    let iter = &mut accounts.iter();
    let counter_info = next_account_info(iter)?;
    let dest_info = next_account_info(iter)?;
    if !dest_info.is_writable {
        return Ok(());
    }
    **dest_info.lamports.borrow_mut() += counter_info.lamports();
    **counter_info.lamports.borrow_mut() = 0;
    Ok(())
}
