package gethexec

import (
	"context"
	"time"

	lightClient "github.com/EspressoSystems/espresso-sequencer-go/light-client"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/arbitrum_types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/offchainlabs/nitro/arbos"
	"github.com/offchainlabs/nitro/util/stopwaiter"
)

const (
	SequencingMode_Espresso    = 0
	SequencingMode_Centralized = 1
)

type SwitchSequencer struct {
	stopwaiter.StopWaiter

	centralized *Sequencer
	espresso    *EspressoSequencer

	maxHotShotDriftTime time.Duration
	switchPollInterval  time.Duration
	lightClient         lightClient.LightClientReaderInterface

	mode int
}

func NewSwitchSequencer(centralized *Sequencer, espresso *EspressoSequencer, l1client bind.ContractBackend, configFetcher SequencerConfigFetcher) (*SwitchSequencer, error) {
	config := configFetcher()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	lightClient, err := arbos.NewMockLightClientReader(common.HexToAddress(config.LightClientAddress), l1client)
	if err != nil {
		return nil, err
	}

	return &SwitchSequencer{
		centralized:         centralized,
		espresso:            espresso,
		lightClient:         lightClient,
		mode:                SequencingMode_Espresso,
		maxHotShotDriftTime: config.MaxHotShotDriftTime,
		switchPollInterval:  config.SwitchPollInterval,
	}, nil
}

func (s *SwitchSequencer) IsRunningEspressoMode() bool {
	return s.mode == SequencingMode_Espresso
}

func (s *SwitchSequencer) SwitchToEspresso(ctx context.Context) error {
	if s.mode == SequencingMode_Espresso {
		return nil
	}
	s.mode = SequencingMode_Espresso
	s.centralized.StopAndWait()
	return s.espresso.Start(ctx)
}

func (s *SwitchSequencer) SwitchToCentralized(ctx context.Context) error {
	if s.mode == SequencingMode_Centralized {
		return nil
	}
	s.mode = SequencingMode_Centralized
	s.espresso.StopAndWait()
	return s.centralized.Start(ctx)
}

func (s *SwitchSequencer) getRunningSequencer() TransactionPublisher {
	if s.IsRunningEspressoMode() {
		return s.espresso
	}
	return s.centralized
}

func (s *SwitchSequencer) PublishTransaction(ctx context.Context, tx *types.Transaction, options *arbitrum_types.ConditionalOptions) error {
	return s.getRunningSequencer().PublishTransaction(ctx, tx, options)
}

func (s *SwitchSequencer) CheckHealth(ctx context.Context) error {
	return s.getRunningSequencer().CheckHealth(ctx)
}

func (s *SwitchSequencer) Initialize(ctx context.Context) error {
	return s.getRunningSequencer().Initialize(ctx)
}

func (s *SwitchSequencer) Start(ctx context.Context) error {
	err := s.getRunningSequencer().Start(ctx)
	if err != nil {
		return err
	}
	s.CallIteratively(func(ctx context.Context) time.Duration {
		espresso := s.lightClient.IsHotShotAvaliable(s.maxHotShotDriftTime)

		var err error
		if s.IsRunningEspressoMode() && !espresso {
			err = s.SwitchToCentralized(ctx)
		} else if !s.IsRunningEspressoMode() && espresso {
			err = s.SwitchToEspresso(ctx)
		}

		if err != nil {
			return 0
		}
		return s.switchPollInterval
	})

	return nil
}

func (s *SwitchSequencer) StopAndWait() {
	s.getRunningSequencer().StopAndWait()
	s.StopWaiter.StopAndWait()
}

func (s *SwitchSequencer) Started() bool {
	return s.getRunningSequencer().Started()
}
