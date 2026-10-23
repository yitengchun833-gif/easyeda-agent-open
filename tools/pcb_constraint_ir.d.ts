export type NetSelector =
  | { kind: 'net'; name: string }
  | { kind: 'netClass'; name: string; nets: string[] };

export type PcbConstraint =
  | { type: 'track_width'; target: NetSelector; min: number; preferred?: number; max?: number; evidenceRef?: string }
  | { type: 'via_size'; target: NetSelector; hole: number; diameterMin: number; diameterPreferred: number; diameterMax: number; evidenceRef?: string }
  | { type: 'via_size'; target: NetSelector; holeMin: number; hole?: never; diameterMin?: never; diameterPreferred?: never; diameterMax?: never; evidenceRef?: string }
  | { type: 'safe_spacing'; target: NetSelector; clearance: number; pairs?: never; evidenceRef?: string }
  | { type: 'safe_spacing'; target: NetSelector; clearance?: never; pairs: Array<{
      objects: ['Track' | 'SMD Pad' | 'TH Pad' | 'Via', 'Track' | 'SMD Pad' | 'TH Pad' | 'Via'];
      clearance: number;
    }>; evidenceRef?: string };

export interface PcbConstraintIR {
  version: 'pcb-constraint/v0';
  unit: 'mm';
  target: { projectUuid: string; documentUuid: string };
  constraints: PcbConstraint[];
}
