package definition

type ConfirmedTransactionJSON struct {
	Transaction TransactionJSON           `json:"transaction"`
	Meta        TransactionStatusMetaJSON `json:"meta"`
}

type TransactionJSON struct {
	Signatures []string    `json:"signatures"`
	Message    MessageJSON `json:"message"`
}

type MessageJSON struct {
	Header struct {
		NumRequiredSignatures       uint32 `json:"num_required_signatures"`
		NumReadonlySignedAccounts   uint32 `json:"num_readonly_signed_accounts"`
		NumReadonlyUnsignedAccounts uint32 `json:"num_readonly_unsigned_accounts"`
	} `json:"header"`
	AccountKeys         []string                  `json:"account_keys"`
	RecentBlockhash     string                    `json:"recent_blockhash"`
	Instructions        []CompiledInstructionJSON `json:"instructions"`
	Versioned           bool                      `json:"versioned"`
	AddressTableLookups []AddressTableLookupJSON  `json:"address_table_lookups,omitempty"`
}

type CompiledInstructionJSON struct {
	ProgramIdIndex uint32  `json:"program_id_index"`
	Accounts       []uint8 `json:"accounts"`
	Data           string  `json:"data"`
}

type AddressTableLookupJSON struct {
	AccountKey      string `json:"account_key"`
	WritableIndexes string `json:"writable_indexes"`
	ReadonlyIndexes string `json:"readonly_indexes"`
}

type TransactionStatusMetaJSON struct {
	Err                     *TransactionErrorJSON   `json:"err,omitempty"`
	Fee                     uint64                  `json:"fee"`
	PreBalances             []uint64                `json:"pre_balances"`
	PostBalances            []uint64                `json:"post_balances"`
	InnerInstructions       []InnerInstructionsJSON `json:"inner_instructions,omitempty"`
	InnerInstructionsNone   bool                    `json:"inner_instructions_none,omitempty"`
	LogMessages             []string                `json:"log_messages,omitempty"`
	LogMessagesNone         bool                    `json:"log_messages_none,omitempty"`
	PreTokenBalances        []TokenBalanceJSON      `json:"pre_token_balances,omitempty"`
	PostTokenBalances       []TokenBalanceJSON      `json:"post_token_balances,omitempty"`
	Rewards                 []RewardJSON            `json:"rewards,omitempty"`
	LoadedWritableAddresses []string                `json:"loaded_writable_addresses,omitempty"`
	LoadedReadonlyAddresses []string                `json:"loaded_readonly_addresses,omitempty"`
	ReturnData              *ReturnDataJSON         `json:"return_data,omitempty"`
	ReturnDataNone          bool                    `json:"return_data_none,omitempty"`
	ComputeUnitsConsumed    *uint64                 `json:"compute_units_consumed,omitempty"`
}

type TransactionErrorJSON struct {
	Err string `json:"err"`
}

type InnerInstructionsJSON struct {
	Index        uint32                 `json:"index"`
	Instructions []InnerInstructionJSON `json:"instructions"`
}

type InnerInstructionJSON struct {
	ProgramIdIndex uint32  `json:"program_id_index"`
	Accounts       []uint8 `json:"accounts"`
	Data           string  `json:"data"`
	StackHeight    *uint32 `json:"stack_height,omitempty"`
}

type TokenBalanceJSON struct {
	AccountIndex  uint32            `json:"account_index"`
	Mint          string            `json:"mint"`
	UiTokenAmount UiTokenAmountJSON `json:"ui_token_amount"`
	Owner         string            `json:"owner"`
	ProgramId     string            `json:"program_id"`
}

type UiTokenAmountJSON struct {
	UiAmount       float64 `json:"ui_amount"`
	Decimals       uint32  `json:"decimals"`
	Amount         string  `json:"amount"`
	UiAmountString string  `json:"ui_amount_string"`
}

type RewardJSON struct {
	Pubkey      string `json:"pubkey"`
	Lamports    int64  `json:"lamports"`
	PostBalance uint64 `json:"post_balance"`
	RewardType  string `json:"reward_type"`
	Commission  string `json:"commission,omitempty"`
}

type ReturnDataJSON struct {
	ProgramId string `json:"program_id"`
	Data      string `json:"data"`
}
