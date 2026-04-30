# Configuração do S3 Backend para Terraform

Este guia explica como configurar o S3 backend para armazenar o estado Terraform no AWS (free-tier).

## O que vais criar no AWS

### 1. S3 Bucket
- **Nome**: `forge-terraform-state-{ACCOUNT_ID}` (exemplo: `forge-terraform-state-123456789012`)
- **Região**: `eu-west-2` (mesma do teu Terraform)
- **Encriptação**: AES256 (automática)
- **Versionamento**: Ativado (para recuperação de versões antigas)
- **Free-tier**: Sim (até 5 GB de armazenamento)

### 2. DynamoDB Table
- **Nome**: `terraform-locks`
- **Partition Key**: `LockID` (String)
- **Billing**: Pay-per-request (free-tier)
- **Região**: `eu-west-2`
- **Propósito**: Evitar que dois deploys alterem o state simultaneamente

## Passos automáticos vs. manuais

### ✅ Automático (no workflow)
O `drift-check.yml` agora cria automaticamente:
- O bucket S3
- A tabela DynamoDB
- Ativa versionamento e encriptação

### ⚙️ Manual (uma única vez)

**Opção A: Via AWS CLI (recomendado)**

```bash
# 1. Obter o ID da tua conta AWS
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "Account ID: $ACCOUNT_ID"

# 2. Criar bucket S3
aws s3api create-bucket \
  --bucket "forge-terraform-state-${ACCOUNT_ID}" \
  --region eu-west-2 \
  --create-bucket-configuration LocationConstraint=eu-west-2

# 3. Ativar versionamento
aws s3api put-bucket-versioning \
  --bucket "forge-terraform-state-${ACCOUNT_ID}" \
  --versioning-configuration Status=Enabled

# 4. Ativar encriptação
aws s3api put-bucket-encryption \
  --bucket "forge-terraform-state-${ACCOUNT_ID}" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'

# 5. Criar tabela DynamoDB
aws dynamodb create-table \
  --table-name terraform-locks \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region eu-west-2
```

**Opção B: Via AWS Console**

1. **S3**:
   - Vai a `Services → S3`
   - Clica "Create bucket"
   - Nome: `forge-terraform-state-{ACCOUNT_ID}`
   - Região: `eu-west-2`
   - Desabilita "Block public access"
   - Clica "Create bucket"
   - Abre o bucket → "Properties" → "Versioning" → Ativa
   - Abre o bucket → "Properties" → "Encryption" → Choose SSE-S3

2. **DynamoDB**:
   - Vai a `Services → DynamoDB`
   - Clica "Create table"
   - Nome: `terraform-locks`
   - Partition key: `LockID` (String)
   - Billing: "Pay-per-request"
   - Clica "Create table"

## Credenciais AWS no GitHub

Cria estes Repository Secrets em `Settings → Secrets and variables → Actions`:

| Secret | Valor | Onde encontrar |
|--------|-------|---|
| `AWS_ACCESS_KEY_ID` | Chave de acesso | [AWS IAM Console](https://console.aws.amazon.com/iamv2/home#/users) |
| `AWS_SECRET_ACCESS_KEY` | Chave secreta | [AWS IAM Console](https://console.aws.amazon.com/iamv2/home#/users) |
| `AWS_REGION` | `eu-west-2` | Região do teu Terraform |
| `FORGE_ADMIN_CIDR` | `188.80.238.138/32` | Do teu `terraform.tfvars` |
| `FORGE_BASE_DOMAIN` | `nforge.space` | Do teu `terraform.tfvars` |

**Como obter credenciais AWS:**
1. Vai a [AWS IAM Console](https://console.aws.amazon.com/iamv2/home#/users)
2. Clica no teu user → "Security credentials"
3. Scroll para "Access keys" → "Create access key"
4. Seleciona "Application running on an AWS compute service"
5. Copia as chaves (salva num ficheiro seguro — não compartilhes!)

⚠️ **Segurança**: Estas credenciais têm acesso total ao teu AWS. Trata-as como passwords de root!

## Próximos passos

1. **Cria o bucket e tabela** (manual ou via CLI acima)
2. **Adiciona os Secrets** no GitHub
3. **Commita o `drift-check.yml` atualizado**
4. **Teste manualmente**: Vai a `Actions → Drift check → Run workflow`
5. **Verifica os logs** para confirmar que:
   - O bucket foi criado/encontrado
   - O DynamoDB foi criado/encontrado
   - O `terraform init` usou o backend S3
   - O `terraform plan` correu com sucesso

## Troubleshooting

### "NoCredentialsError"
- Verifica se `AWS_ACCESS_KEY_ID` e `AWS_SECRET_ACCESS_KEY` estão corretos no GitHub Secrets

### "Bucket already exists"
- Isso é OK — significa que o bucket foi criado anteriormente
- O workflow continua normalmente

### "Access Denied"
- Verifica se a chave AWS tem permissões para S3 e DynamoDB
- Cria uma nova chave com as permissões corretas

### "terraform init" falha com backend error
- Verifica se o `backend-config.tfvars` foi criado corretamente
- Verifica se o Account ID está correto (deve corresponder ao bucket)

## Custos

- **S3**: ~$0.023 por GB/mês (5 GB free)
- **DynamoDB**: ~$0.25 por 1M requests (pay-per-request)
- **Drift check semanal**: Negligenciável (~0.001 requests/semana)

**Total esperado**: **$0** enquanto estiveres no free-tier.

## Próximas melhorias

Depois de tudo a funcionar:
- [ ] Adiciona `backup` automático do state (S3 replication)
- [ ] Monitora `drift-check` com alertas CloudWatch
- [ ] Configura `terraform destroy` proteção com environment protection rules
