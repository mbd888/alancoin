import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api-client";
import { toast } from "sonner";

export interface CostCenter {
  id: string;
  name: string;
  department: string;
  monthlyBudget: string;
  warnAtPercent: number;
  active: boolean;
}

export interface PeriodSummary {
  costCenterName: string;
  department: string;
  totalSpend: string;
  txCount: number;
  topService: string;
  budgetUsedPct: number;
}

export interface ChargebackReport {
  period: string;
  totalSpend: string;
  costCenterCount: number;
  summaries: PeriodSummary[];
}

export type CostCentersResponse = {
  costCenters: CostCenter[];
  count: number;
};

export function useCostCenters() {
  return useQuery({
    queryKey: ["chargeback", "cost-centers"],
    queryFn: () => api.get<CostCentersResponse>("/chargeback/cost-centers"),
  });
}

export function useChargebackReport() {
  return useQuery({
    queryKey: ["chargeback", "report"],
    queryFn: () => api.get<{ report: ChargebackReport }>("/chargeback/reports"),
  });
}

export function useCreateCostCenter(onSuccess?: () => void) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      api.post("/chargeback/cost-centers", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["chargeback", "cost-centers"] });
      queryClient.invalidateQueries({ queryKey: ["chargeback", "report"] });
      toast.success("Cost center created");
      onSuccess?.();
    },
    onError: () => toast.error("Failed to create cost center"),
  });
}
